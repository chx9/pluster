package engine

import (
	gnet "github.com/panjf2000/gnet/v2"

	"github.com/pluster/pluster/pkg/proto"
)

func (h *ProxyHandler) startSubscribe(c gnet.Conn, cctx *clientCtx, req *proto.Request) gnet.Action {
	hub := h.getPubsubHub(c.EventLoop())
	hub.subscribe(c, cctx, req)
	return gnet.None
}

func (h *ProxyHandler) handleInSubscribe(c gnet.Conn, cctx *clientCtx, req *proto.Request, name string) gnet.Action {
	switch name {
	case "SUBSCRIBE", "PSUBSCRIBE", "UNSUBSCRIBE", "PUNSUBSCRIBE", "PING", "RESET", "QUIT":
		if name == "RESET" {
			h.resetClientState(c, cctx)
			h.writeImmediate(c, cctx, proto.EncodeSimpleString("RESET"))
			return gnet.None
		}
		if name == "QUIT" {
			_, _ = c.Write(proto.EncodeSimpleString("OK"))
			return gnet.Close
		}
		if name == "PING" {
			writeHubMsgToClient(c, proto.EncodeValue(&proto.Value{
				Type: proto.TypeArray,
				Array: []*proto.Value{
					proto.BulkValue([]byte("pong")),
					proto.BulkValue([]byte{}),
				},
			}))
			return gnet.None
		}
		if name == "UNSUBSCRIBE" || name == "PUNSUBSCRIBE" {
			hub := h.getPubsubHub(c.EventLoop())
			hub.unsubscribe(c, cctx, req)
			return gnet.None
		}
		hub := h.getPubsubHub(c.EventLoop())
		hub.subscribe(c, cctx, req)
		return gnet.None
	default:
		h.writeImmediate(c, cctx, proto.EncodeError("ERR Command not allowed inside a subscription context"))
		return gnet.None
	}
}
