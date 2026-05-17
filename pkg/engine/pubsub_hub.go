package engine

import (
	"context"
	"net"
	"path/filepath"
	"strings"

	gnet "github.com/panjf2000/gnet/v2"

	"github.com/pluster/pluster/pkg/proto"
)

type subEntry struct {
	clientConn gnet.Conn
	cctx       *clientCtx
	pattern    string
	isGlob     bool
}

type pubsubHub struct {
	handler        *ProxyHandler
	el             gnet.EventLoop
	backendConn    gnet.Conn
	dialing        bool
	pendingClients []pendingSubClient
	subs           map[string][]subEntry
	clientSubs     map[gnet.Conn]map[string]struct{}
}

type pendingSubClient struct {
	clientConn gnet.Conn
	cctx       *clientCtx
	req        *proto.Request
}

func newPubsubHub(el gnet.EventLoop, h *ProxyHandler) *pubsubHub {
	return &pubsubHub{
		handler:    h,
		el:         el,
		subs:       make(map[string][]subEntry),
		clientSubs: make(map[gnet.Conn]map[string]struct{}),
	}
}

func (hub *pubsubHub) subscribe(clientConn gnet.Conn, cctx *clientCtx, req *proto.Request) {
	cctx.state = stateSubscribe

	if hub.backendConn == nil && !hub.dialing {
		hub.dialSharedBackend()
	}

	isGlob := req.Cmd == "PSUBSCRIBE"
	for i := 1; i < len(req.Args); i++ {
		hub.addSub(clientConn, cctx, string(req.Args[i]), isGlob)
	}

	if hub.backendConn != nil {
		hub.forwardToBackend(req)
	} else {
		hub.pendingClients = append(hub.pendingClients, pendingSubClient{
			clientConn: clientConn,
			cctx:       cctx,
			req:        req,
		})
	}
}

func (hub *pubsubHub) unsubscribe(clientConn gnet.Conn, cctx *clientCtx, req *proto.Request) {
	if len(req.Args) == 1 {
		hub.removeAllSubs(clientConn)
	} else {
		for i := 1; i < len(req.Args); i++ {
			hub.removeSub(clientConn, string(req.Args[i]))
		}
	}

	if len(hub.clientSubs[clientConn]) == 0 {
		delete(hub.clientSubs, clientConn)
		cctx.state = stateNormal
	}

	if hub.backendConn != nil {
		hub.forwardToBackend(req)
	} else {
		subType := "unsubscribe"
		if req.Cmd == "PUNSUBSCRIBE" {
			subType = "punsubscribe"
		}
		count := int64(len(hub.clientSubs[clientConn]))
		for i := 1; i < len(req.Args); i++ {
			ch := req.Args[i]
			reply := proto.EncodeValue(&proto.Value{
				Type: proto.TypeArray,
				Array: []*proto.Value{
					proto.BulkValue([]byte(subType)),
					proto.BulkValue(ch),
					{Type: proto.TypeInteger, Integer: count},
				},
			})
			writeHubMsgToClient(clientConn, reply)
		}
	}
}

func (hub *pubsubHub) removeClient(clientConn gnet.Conn) {
	hub.removeAllSubs(clientConn)
	delete(hub.clientSubs, clientConn)
}

func (hub *pubsubHub) onSharedBackendOpen(c gnet.Conn) {
	hub.dialing = false
	hub.backendConn = c

	if hub.handler.cfg.Password != "" {
		var authCmd []byte
		if hub.handler.cfg.Username != "" {
			authCmd = proto.EncodeCommandBytes(
				[]byte("AUTH"),
				[]byte(hub.handler.cfg.Username),
				[]byte(hub.handler.cfg.Password),
			)
		} else {
			authCmd = proto.EncodeCommandBytes([]byte("AUTH"), []byte(hub.handler.cfg.Password))
		}
		_, _ = c.Write(authCmd)
	}

	_, _ = c.Write(proto.EncodeCommandBytes([]byte("PSUBSCRIBE"), []byte("*")))

	for _, pc := range hub.pendingClients {
		hub.forwardToBackend(pc.req)
	}
	hub.pendingClients = hub.pendingClients[:0]
}

func (hub *pubsubHub) onSharedBackendClose() {
	hub.backendConn = nil
	hub.dialing = false

	errMsg := proto.EncodeError("ERR pubsub backend connection lost; please re-subscribe")
	notified := make(map[gnet.Conn]struct{})
	for _, entries := range hub.subs {
		for _, e := range entries {
			if _, done := notified[e.clientConn]; done {
				continue
			}
			notified[e.clientConn] = struct{}{}
			writeHubMsgToClient(e.clientConn, errMsg)
			if e.cctx != nil {
				e.cctx.state = stateNormal
			}
		}
	}
	hub.subs = make(map[string][]subEntry)
	hub.clientSubs = make(map[gnet.Conn]map[string]struct{})
}

func (hub *pubsubHub) onSharedBackendTraffic(c gnet.Conn) gnet.Action {
	for {
		buf, _ := c.Peek(-1)
		if len(buf) == 0 {
			return gnet.None
		}
		val, consumed, err := proto.ParseValue(buf)
		if err != nil {
			return gnet.Close
		}
		if consumed == 0 {
			return gnet.None
		}
		_, _ = c.Discard(consumed)
		hub.dispatchMessage(val)
	}
}

func (hub *pubsubHub) dispatchMessage(val *proto.Value) {
	if val == nil || val.Type != proto.TypeArray || len(val.Array) < 3 {
		return
	}

	msgType := strings.ToLower(string(val.Array[0].Str))

	switch msgType {
	case "psubscribe", "punsubscribe":
		return

	case "subscribe", "unsubscribe":
		if len(val.Array) < 3 {
			return
		}
		channel := string(val.Array[1].Str)
		encoded := proto.EncodeValue(val)
		for _, e := range hub.subs[channel] {
			if !e.isGlob {
				writeHubMsgToClient(e.clientConn, encoded)
			}
		}

	case "message":
		if len(val.Array) < 3 {
			return
		}
		channel := string(val.Array[1].Str)
		encoded := proto.EncodeValue(val)
		for _, e := range hub.subs[channel] {
			if !e.isGlob {
				writeHubMsgToClient(e.clientConn, encoded)
			}
		}
		for pattern, entries := range hub.subs {
			for _, e := range entries {
				if e.isGlob && globMatch(pattern, channel) {
					pmsg := proto.EncodeValue(&proto.Value{
						Type: proto.TypeArray,
						Array: []*proto.Value{
							proto.BulkValue([]byte("pmessage")),
							proto.BulkValue([]byte(pattern)),
							proto.BulkValue([]byte(channel)),
							proto.BulkValue(val.Array[2].Str),
						},
					})
					writeHubMsgToClient(e.clientConn, pmsg)
				}
			}
		}

	case "pmessage":
		if len(val.Array) < 4 {
			return
		}
		channel := string(val.Array[2].Str)
		payload := val.Array[3].Str
		for pattern, entries := range hub.subs {
			for _, e := range entries {
				if e.isGlob && globMatch(pattern, channel) {
					pmsg := proto.EncodeValue(&proto.Value{
						Type: proto.TypeArray,
						Array: []*proto.Value{
							proto.BulkValue([]byte("pmessage")),
							proto.BulkValue([]byte(pattern)),
							proto.BulkValue([]byte(channel)),
							proto.BulkValue(payload),
						},
					})
					writeHubMsgToClient(e.clientConn, pmsg)
				}
			}
		}
	}
}

func (hub *pubsubHub) addSub(clientConn gnet.Conn, cctx *clientCtx, pattern string, isGlob bool) {
	for _, e := range hub.subs[pattern] {
		if e.clientConn == clientConn {
			return
		}
	}
	hub.subs[pattern] = append(hub.subs[pattern], subEntry{
		clientConn: clientConn,
		cctx:       cctx,
		pattern:    pattern,
		isGlob:     isGlob,
	})
	if hub.clientSubs[clientConn] == nil {
		hub.clientSubs[clientConn] = make(map[string]struct{})
	}
	hub.clientSubs[clientConn][pattern] = struct{}{}
}

func (hub *pubsubHub) removeSub(clientConn gnet.Conn, pattern string) {
	entries := hub.subs[pattern]
	for i, e := range entries {
		if e.clientConn == clientConn {
			hub.subs[pattern] = append(entries[:i], entries[i+1:]...)
			break
		}
	}
	if len(hub.subs[pattern]) == 0 {
		delete(hub.subs, pattern)
	}
	if cs := hub.clientSubs[clientConn]; cs != nil {
		delete(cs, pattern)
	}
}

func (hub *pubsubHub) removeAllSubs(clientConn gnet.Conn) {
	for p := range hub.clientSubs[clientConn] {
		entries := hub.subs[p]
		for i, e := range entries {
			if e.clientConn == clientConn {
				hub.subs[p] = append(entries[:i], entries[i+1:]...)
				break
			}
		}
		if len(hub.subs[p]) == 0 {
			delete(hub.subs, p)
		}
	}
}

func (hub *pubsubHub) forwardToBackend(req *proto.Request) {
	if hub.backendConn == nil {
		return
	}
	_, _ = hub.backendConn.Write(proto.EncodeCommandBytes(req.Args...))
}

func (hub *pubsubHub) dialSharedBackend() {
	hub.dialing = true

	topo := hub.handler.topoMgr.LoadTopo()
	masters := topo.AllMasters()
	if len(masters) == 0 {
		hub.dialing = false
		return
	}
	addr := masters[0].Addr

	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		hub.dialing = false
		return
	}

	bctx := &backendCtx{
		addr:        addr,
		isDedicated: true,
		isSubConn:   true,
		isSharedHub: true,
	}
	ctx := gnet.NewContext(context.Background(), bctx)
	resCh, err := hub.el.Register(ctx, tcpAddr)
	if err != nil {
		hub.dialing = false
		return
	}

	go func() {
		res := <-resCh
		if res.Err != nil {
			_ = hub.el.Execute(context.Background(), gnet.RunnableFunc(func(_ context.Context) error {
				hub.dialing = false
				return nil
			}))
		}
	}()
}

func globMatch(pattern, subject string) bool {
	matched, err := filepath.Match(pattern, subject)
	return err == nil && matched
}

func writeHubMsgToClient(c gnet.Conn, data []byte) {
	_ = c.AsyncWrite(data, nil)
}
