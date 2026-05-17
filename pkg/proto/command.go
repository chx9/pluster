package proto

import "bytes"

type CmdFlag uint32

const (
	FlagWrite       CmdFlag = 1 << 0
	FlagRead        CmdFlag = 1 << 1
	FlagAdmin       CmdFlag = 1 << 2
	FlagPubSub      CmdFlag = 1 << 3
	FlagNoKey       CmdFlag = 1 << 4
	FlagMultiKey    CmdFlag = 1 << 5
	FlagBlocking    CmdFlag = 1 << 6
	FlagTransaction CmdFlag = 1 << 7
	FlagScript      CmdFlag = 1 << 8
	FlagAllNodes    CmdFlag = 1 << 9
	FlagNotAllowed  CmdFlag = 1 << 10
)

type KeySpec struct {
	FirstKey int
	LastKey  int
	Step     int
}

type CmdInfo struct {
	Name    string
	Arity   int
	Flags   CmdFlag
	KeySpec KeySpec
}

var cmdTable = map[string]*CmdInfo{
	"GET":              {Name: "GET", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"SET":              {Name: "SET", Arity: -3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"DEL":              {Name: "DEL", Arity: -2, Flags: FlagWrite | FlagMultiKey, KeySpec: KeySpec{1, -1, 1}},
	"UNLINK":           {Name: "UNLINK", Arity: -2, Flags: FlagWrite | FlagMultiKey, KeySpec: KeySpec{1, -1, 1}},
	"MGET":             {Name: "MGET", Arity: -2, Flags: FlagRead | FlagMultiKey, KeySpec: KeySpec{1, -1, 1}},
	"MSET":             {Name: "MSET", Arity: -3, Flags: FlagWrite | FlagMultiKey, KeySpec: KeySpec{1, -1, 2}},
	"MSETNX":           {Name: "MSETNX", Arity: -3, Flags: FlagWrite | FlagMultiKey, KeySpec: KeySpec{1, -1, 2}},
	"EXISTS":           {Name: "EXISTS", Arity: -2, Flags: FlagRead | FlagMultiKey, KeySpec: KeySpec{1, -1, 1}},
	"TOUCH":            {Name: "TOUCH", Arity: -2, Flags: FlagRead | FlagMultiKey, KeySpec: KeySpec{1, -1, 1}},
	"APPEND":           {Name: "APPEND", Arity: 3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"GETSET":           {Name: "GETSET", Arity: 3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"GETDEL":           {Name: "GETDEL", Arity: 2, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"GETEX":            {Name: "GETEX", Arity: -2, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"SETNX":            {Name: "SETNX", Arity: 3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"SETEX":            {Name: "SETEX", Arity: 4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"PSETEX":           {Name: "PSETEX", Arity: 4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"GETRANGE":         {Name: "GETRANGE", Arity: 4, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"SETRANGE":         {Name: "SETRANGE", Arity: 4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"STRLEN":           {Name: "STRLEN", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"INCR":             {Name: "INCR", Arity: 2, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"INCRBY":           {Name: "INCRBY", Arity: 3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"INCRBYFLOAT":      {Name: "INCRBYFLOAT", Arity: 3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"DECR":             {Name: "DECR", Arity: 2, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"DECRBY":           {Name: "DECRBY", Arity: 3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"HGET":             {Name: "HGET", Arity: 3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"HSET":             {Name: "HSET", Arity: -4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"HSETNX":           {Name: "HSETNX", Arity: 4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"HMGET":            {Name: "HMGET", Arity: -3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"HMSET":            {Name: "HMSET", Arity: -4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"HDEL":             {Name: "HDEL", Arity: -3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"HEXISTS":          {Name: "HEXISTS", Arity: 3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"HGETALL":          {Name: "HGETALL", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"HKEYS":            {Name: "HKEYS", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"HVALS":            {Name: "HVALS", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"HLEN":             {Name: "HLEN", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"HSTRLEN":          {Name: "HSTRLEN", Arity: 3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"HRANDFIELD":       {Name: "HRANDFIELD", Arity: -2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"HINCRBY":          {Name: "HINCRBY", Arity: 4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"HINCRBYFLOAT":     {Name: "HINCRBYFLOAT", Arity: 4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"HSCAN":            {Name: "HSCAN", Arity: -3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"LPUSH":            {Name: "LPUSH", Arity: -3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"LPUSHX":           {Name: "LPUSHX", Arity: -3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"RPUSH":            {Name: "RPUSH", Arity: -3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"RPUSHX":           {Name: "RPUSHX", Arity: -3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"LPOP":             {Name: "LPOP", Arity: -2, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"RPOP":             {Name: "RPOP", Arity: -2, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"LLEN":             {Name: "LLEN", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"LRANGE":           {Name: "LRANGE", Arity: 4, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"LINDEX":           {Name: "LINDEX", Arity: 3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"LSET":             {Name: "LSET", Arity: 4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"LINSERT":          {Name: "LINSERT", Arity: 5, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"LREM":             {Name: "LREM", Arity: 4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"LTRIM":            {Name: "LTRIM", Arity: 4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"LPOS":             {Name: "LPOS", Arity: -3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"LMOVE":            {Name: "LMOVE", Arity: 5, Flags: FlagWrite, KeySpec: KeySpec{1, 2, 1}},
	"RPOPLPUSH":        {Name: "RPOPLPUSH", Arity: 3, Flags: FlagWrite | FlagNotAllowed, KeySpec: KeySpec{1, 2, 1}},
	"LMPOP":            {Name: "LMPOP", Arity: -4, Flags: FlagWrite, KeySpec: KeySpec{2, -1, 1}},
	"BLPOP":            {Name: "BLPOP", Arity: -3, Flags: FlagWrite | FlagBlocking, KeySpec: KeySpec{1, -2, 1}},
	"BRPOP":            {Name: "BRPOP", Arity: -3, Flags: FlagWrite | FlagBlocking, KeySpec: KeySpec{1, -2, 1}},
	"SADD":             {Name: "SADD", Arity: -3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"SREM":             {Name: "SREM", Arity: -3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"SMEMBERS":         {Name: "SMEMBERS", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"SISMEMBER":        {Name: "SISMEMBER", Arity: 3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"SMISMEMBER":       {Name: "SMISMEMBER", Arity: -3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"SCARD":            {Name: "SCARD", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"SPOP":             {Name: "SPOP", Arity: -2, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"SRANDMEMBER":      {Name: "SRANDMEMBER", Arity: -2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"SMOVE":            {Name: "SMOVE", Arity: 4, Flags: FlagWrite | FlagNotAllowed, KeySpec: KeySpec{1, 2, 1}},
	"SUNION":           {Name: "SUNION", Arity: -2, Flags: FlagRead | FlagMultiKey | FlagNotAllowed, KeySpec: KeySpec{1, -1, 1}},
	"SINTER":           {Name: "SINTER", Arity: -2, Flags: FlagRead | FlagMultiKey | FlagNotAllowed, KeySpec: KeySpec{1, -1, 1}},
	"SDIFF":            {Name: "SDIFF", Arity: -2, Flags: FlagRead | FlagMultiKey | FlagNotAllowed, KeySpec: KeySpec{1, -1, 1}},
	"SUNIONSTORE":      {Name: "SUNIONSTORE", Arity: -3, Flags: FlagWrite | FlagMultiKey | FlagNotAllowed, KeySpec: KeySpec{1, -1, 1}},
	"SINTERSTORE":      {Name: "SINTERSTORE", Arity: -3, Flags: FlagWrite | FlagMultiKey | FlagNotAllowed, KeySpec: KeySpec{1, -1, 1}},
	"SDIFFSTORE":       {Name: "SDIFFSTORE", Arity: -3, Flags: FlagWrite | FlagMultiKey | FlagNotAllowed, KeySpec: KeySpec{1, -1, 1}},
	"SSCAN":            {Name: "SSCAN", Arity: -3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"SINTERCARD":       {Name: "SINTERCARD", Arity: -3, Flags: FlagRead | FlagMultiKey | FlagNotAllowed, KeySpec: KeySpec{2, -1, 1}},
	"ZADD":             {Name: "ZADD", Arity: -4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"ZREM":             {Name: "ZREM", Arity: -3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"ZINCRBY":          {Name: "ZINCRBY", Arity: 4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"ZRANK":            {Name: "ZRANK", Arity: -3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZREVRANK":         {Name: "ZREVRANK", Arity: -3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZSCORE":           {Name: "ZSCORE", Arity: 3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZMSCORE":          {Name: "ZMSCORE", Arity: -3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZCARD":            {Name: "ZCARD", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZCOUNT":           {Name: "ZCOUNT", Arity: 4, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZLEXCOUNT":        {Name: "ZLEXCOUNT", Arity: 4, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZRANGE":           {Name: "ZRANGE", Arity: -4, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZREVRANGE":        {Name: "ZREVRANGE", Arity: -4, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZRANGEBYSCORE":    {Name: "ZRANGEBYSCORE", Arity: -4, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZREVRANGEBYSCORE": {Name: "ZREVRANGEBYSCORE", Arity: -4, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZRANGEBYLEX":      {Name: "ZRANGEBYLEX", Arity: -4, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZREVRANGEBYLEX":   {Name: "ZREVRANGEBYLEX", Arity: -4, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZRANGESTORE":      {Name: "ZRANGESTORE", Arity: -5, Flags: FlagWrite, KeySpec: KeySpec{1, 2, 1}},
	"ZREMRANGEBYSCORE": {Name: "ZREMRANGEBYSCORE", Arity: 4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"ZREMRANGEBYRANK":  {Name: "ZREMRANGEBYRANK", Arity: 4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"ZREMRANGEBYLEX":   {Name: "ZREMRANGEBYLEX", Arity: 4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"ZPOPMIN":          {Name: "ZPOPMIN", Arity: -2, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"ZPOPMAX":          {Name: "ZPOPMAX", Arity: -2, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"ZRANDMEMBER":      {Name: "ZRANDMEMBER", Arity: -2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZSCAN":            {Name: "ZSCAN", Arity: -3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"ZUNIONSTORE":      {Name: "ZUNIONSTORE", Arity: -4, Flags: FlagWrite | FlagMultiKey | FlagNotAllowed, KeySpec: KeySpec{1, 1, 1}},
	"ZINTERSTORE":      {Name: "ZINTERSTORE", Arity: -4, Flags: FlagWrite | FlagMultiKey | FlagNotAllowed, KeySpec: KeySpec{1, 1, 1}},
	"ZDIFFSTORE":       {Name: "ZDIFFSTORE", Arity: -4, Flags: FlagWrite | FlagMultiKey | FlagNotAllowed, KeySpec: KeySpec{1, 1, 1}},
	"ZUNION":           {Name: "ZUNION", Arity: -3, Flags: FlagRead | FlagMultiKey | FlagNotAllowed, KeySpec: KeySpec{2, -1, 1}},
	"ZINTER":           {Name: "ZINTER", Arity: -3, Flags: FlagRead | FlagMultiKey | FlagNotAllowed, KeySpec: KeySpec{2, -1, 1}},
	"ZDIFF":            {Name: "ZDIFF", Arity: -3, Flags: FlagRead | FlagMultiKey | FlagNotAllowed, KeySpec: KeySpec{2, -1, 1}},
	"BZPOPMIN":         {Name: "BZPOPMIN", Arity: -3, Flags: FlagWrite | FlagBlocking, KeySpec: KeySpec{1, -2, 1}},
	"BZPOPMAX":         {Name: "BZPOPMAX", Arity: -3, Flags: FlagWrite | FlagBlocking, KeySpec: KeySpec{1, -2, 1}},
	"BLMOVE":           {Name: "BLMOVE", Arity: 6, Flags: FlagWrite | FlagBlocking, KeySpec: KeySpec{1, 2, 1}},
	"BLMPOP":           {Name: "BLMPOP", Arity: -5, Flags: FlagWrite | FlagBlocking, KeySpec: KeySpec{0, 0, 0}},
	"BZMPOP":           {Name: "BZMPOP", Arity: -5, Flags: FlagWrite | FlagBlocking, KeySpec: KeySpec{0, 0, 0}},
	"EXPIRE":           {Name: "EXPIRE", Arity: -3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"EXPIREAT":         {Name: "EXPIREAT", Arity: 3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"PEXPIRE":          {Name: "PEXPIRE", Arity: 3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"PEXPIREAT":        {Name: "PEXPIREAT", Arity: 3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"EXPIRETIME":       {Name: "EXPIRETIME", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"PEXPIRETIME":      {Name: "PEXPIRETIME", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"TTL":              {Name: "TTL", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"PTTL":             {Name: "PTTL", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"PERSIST":          {Name: "PERSIST", Arity: 2, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"TYPE":             {Name: "TYPE", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"RENAME":           {Name: "RENAME", Arity: 3, Flags: FlagWrite | FlagNotAllowed, KeySpec: KeySpec{1, 2, 1}},
	"RENAMENX":         {Name: "RENAMENX", Arity: 3, Flags: FlagWrite | FlagNotAllowed, KeySpec: KeySpec{1, 2, 1}},
	"COPY":             {Name: "COPY", Arity: -3, Flags: FlagWrite, KeySpec: KeySpec{1, 2, 1}},
	"MOVE":             {Name: "MOVE", Arity: 3, Flags: FlagWrite | FlagNotAllowed, KeySpec: KeySpec{1, 1, 1}},
	"DUMP":             {Name: "DUMP", Arity: 2, Flags: FlagRead | FlagNotAllowed, KeySpec: KeySpec{1, 1, 1}},
	"RESTORE":          {Name: "RESTORE", Arity: -4, Flags: FlagWrite | FlagNotAllowed, KeySpec: KeySpec{1, 1, 1}},
	"OBJECT":           {Name: "OBJECT", Arity: -2, Flags: FlagRead, KeySpec: KeySpec{2, 2, 1}},
	"SORT":             {Name: "SORT", Arity: -2, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"SCAN":             {Name: "SCAN", Arity: -2, Flags: FlagRead | FlagAllNodes, KeySpec: KeySpec{0, 0, 0}},
	"PFADD":            {Name: "PFADD", Arity: -2, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"PFCOUNT":          {Name: "PFCOUNT", Arity: -2, Flags: FlagRead | FlagMultiKey, KeySpec: KeySpec{1, -1, 1}},
	"PFMERGE":          {Name: "PFMERGE", Arity: -2, Flags: FlagWrite | FlagMultiKey | FlagNotAllowed, KeySpec: KeySpec{1, -1, 1}},
	"GEOADD":           {Name: "GEOADD", Arity: -5, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"GEODIST":          {Name: "GEODIST", Arity: -4, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"GEOHASH":          {Name: "GEOHASH", Arity: -3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"GEOPOS":           {Name: "GEOPOS", Arity: -2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"GEORADIUS":        {Name: "GEORADIUS", Arity: -6, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"GEORADIUSBYMEMBER": {Name: "GEORADIUSBYMEMBER", Arity: -5, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"GEOSEARCH":        {Name: "GEOSEARCH", Arity: -7, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"GEOSEARCHSTORE":   {Name: "GEOSEARCHSTORE", Arity: -8, Flags: FlagWrite, KeySpec: KeySpec{1, 2, 1}},
	"BITCOUNT":         {Name: "BITCOUNT", Arity: -2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"BITPOS":           {Name: "BITPOS", Arity: -3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"BITOP":            {Name: "BITOP", Arity: -4, Flags: FlagWrite | FlagMultiKey, KeySpec: KeySpec{2, -1, 1}},
	"BITFIELD":         {Name: "BITFIELD", Arity: -2, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"SETBIT":           {Name: "SETBIT", Arity: 4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"GETBIT":           {Name: "GETBIT", Arity: 3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"XADD":             {Name: "XADD", Arity: -5, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"XREAD":            {Name: "XREAD", Arity: -4, Flags: FlagRead | FlagMultiKey, KeySpec: KeySpec{0, 0, 0}},
	"XREADGROUP":       {Name: "XREADGROUP", Arity: -7, Flags: FlagWrite | FlagMultiKey, KeySpec: KeySpec{0, 0, 0}},
	"XRANGE":           {Name: "XRANGE", Arity: -4, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"XREVRANGE":        {Name: "XREVRANGE", Arity: -4, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"XLEN":             {Name: "XLEN", Arity: 2, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"XACK":             {Name: "XACK", Arity: -4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"XDEL":             {Name: "XDEL", Arity: -3, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"XTRIM":            {Name: "XTRIM", Arity: -4, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"XINFO":            {Name: "XINFO", Arity: -2, Flags: FlagRead, KeySpec: KeySpec{2, 2, 1}},
	"XGROUP":           {Name: "XGROUP", Arity: -2, Flags: FlagWrite, KeySpec: KeySpec{2, 2, 1}},
	"XCLAIM":           {Name: "XCLAIM", Arity: -6, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"XAUTOCLAIM":       {Name: "XAUTOCLAIM", Arity: -7, Flags: FlagWrite, KeySpec: KeySpec{1, 1, 1}},
	"XPENDING":         {Name: "XPENDING", Arity: -3, Flags: FlagRead, KeySpec: KeySpec{1, 1, 1}},
	"EVAL":             {Name: "EVAL", Arity: -3, Flags: FlagScript, KeySpec: KeySpec{3, 0, 1}},
	"EVALSHA":          {Name: "EVALSHA", Arity: -3, Flags: FlagScript, KeySpec: KeySpec{3, 0, 1}},
	"EVAL_RO":          {Name: "EVAL_RO", Arity: -3, Flags: FlagScript | FlagRead, KeySpec: KeySpec{3, 0, 1}},
	"EVALSHA_RO":       {Name: "EVALSHA_RO", Arity: -3, Flags: FlagScript | FlagRead, KeySpec: KeySpec{3, 0, 1}},
	"SCRIPT":           {Name: "SCRIPT", Arity: -2, Flags: FlagScript | FlagAdmin, KeySpec: KeySpec{0, 0, 0}},
	"PUBLISH":          {Name: "PUBLISH", Arity: 3, Flags: FlagPubSub, KeySpec: KeySpec{0, 0, 0}},
	"SUBSCRIBE":        {Name: "SUBSCRIBE", Arity: -2, Flags: FlagPubSub, KeySpec: KeySpec{0, 0, 0}},
	"UNSUBSCRIBE":      {Name: "UNSUBSCRIBE", Arity: -1, Flags: FlagPubSub, KeySpec: KeySpec{0, 0, 0}},
	"PSUBSCRIBE":       {Name: "PSUBSCRIBE", Arity: -2, Flags: FlagPubSub, KeySpec: KeySpec{0, 0, 0}},
	"PUNSUBSCRIBE":     {Name: "PUNSUBSCRIBE", Arity: -1, Flags: FlagPubSub, KeySpec: KeySpec{0, 0, 0}},
	"PUBSUB":           {Name: "PUBSUB", Arity: -2, Flags: FlagPubSub | FlagRead, KeySpec: KeySpec{0, 0, 0}},
	"MULTI":            {Name: "MULTI", Arity: 1, Flags: FlagTransaction, KeySpec: KeySpec{0, 0, 0}},
	"EXEC":             {Name: "EXEC", Arity: 1, Flags: FlagTransaction, KeySpec: KeySpec{0, 0, 0}},
	"DISCARD":          {Name: "DISCARD", Arity: 1, Flags: FlagTransaction, KeySpec: KeySpec{0, 0, 0}},
	"WATCH":            {Name: "WATCH", Arity: -2, Flags: FlagTransaction, KeySpec: KeySpec{1, -1, 1}},
	"UNWATCH":          {Name: "UNWATCH", Arity: 1, Flags: FlagTransaction, KeySpec: KeySpec{0, 0, 0}},
	"PING":             {Name: "PING", Arity: -1, Flags: FlagNoKey, KeySpec: KeySpec{0, 0, 0}},
	"ECHO":             {Name: "ECHO", Arity: 2, Flags: FlagNoKey, KeySpec: KeySpec{0, 0, 0}},
	"SELECT":           {Name: "SELECT", Arity: 2, Flags: FlagNoKey, KeySpec: KeySpec{0, 0, 0}},
	"AUTH":             {Name: "AUTH", Arity: -2, Flags: FlagNoKey, KeySpec: KeySpec{0, 0, 0}},
	"QUIT":             {Name: "QUIT", Arity: 1, Flags: FlagNoKey, KeySpec: KeySpec{0, 0, 0}},
	"RESET":            {Name: "RESET", Arity: 1, Flags: FlagNoKey, KeySpec: KeySpec{0, 0, 0}},
	"CLIENT":           {Name: "CLIENT", Arity: -2, Flags: FlagNoKey, KeySpec: KeySpec{0, 0, 0}},
	"DBSIZE":           {Name: "DBSIZE", Arity: 1, Flags: FlagRead | FlagAllNodes, KeySpec: KeySpec{0, 0, 0}},
	"KEYS":             {Name: "KEYS", Arity: 2, Flags: FlagRead | FlagAllNodes, KeySpec: KeySpec{0, 0, 0}},
	"RANDOMKEY":        {Name: "RANDOMKEY", Arity: 1, Flags: FlagRead | FlagAllNodes | FlagNotAllowed, KeySpec: KeySpec{0, 0, 0}},
	"FLUSHDB":          {Name: "FLUSHDB", Arity: -1, Flags: FlagWrite | FlagAdmin | FlagAllNodes, KeySpec: KeySpec{0, 0, 0}},
	"FLUSHALL":         {Name: "FLUSHALL", Arity: -1, Flags: FlagWrite | FlagAdmin | FlagAllNodes, KeySpec: KeySpec{0, 0, 0}},
	"INFO":             {Name: "INFO", Arity: -1, Flags: FlagRead | FlagAdmin, KeySpec: KeySpec{0, 0, 0}},
	"CONFIG":           {Name: "CONFIG", Arity: -2, Flags: FlagAdmin, KeySpec: KeySpec{0, 0, 0}},
	"DEBUG":            {Name: "DEBUG", Arity: -2, Flags: FlagAdmin, KeySpec: KeySpec{0, 0, 0}},
	"CLUSTER":          {Name: "CLUSTER", Arity: -2, Flags: FlagAdmin, KeySpec: KeySpec{0, 0, 0}},
	"COMMAND":          {Name: "COMMAND", Arity: -1, Flags: FlagNoKey, KeySpec: KeySpec{0, 0, 0}},
	"LATENCY":          {Name: "LATENCY", Arity: -2, Flags: FlagAdmin, KeySpec: KeySpec{0, 0, 0}},
	"SLOWLOG":          {Name: "SLOWLOG", Arity: -2, Flags: FlagAdmin, KeySpec: KeySpec{0, 0, 0}},
	"TIME":             {Name: "TIME", Arity: 1, Flags: FlagRead | FlagNoKey, KeySpec: KeySpec{0, 0, 0}},
	"WAIT":             {Name: "WAIT", Arity: 3, Flags: FlagNoKey, KeySpec: KeySpec{0, 0, 0}},
	"HELLO":            {Name: "HELLO", Arity: -1, Flags: FlagNoKey, KeySpec: KeySpec{0, 0, 0}},
	"READONLY":         {Name: "READONLY", Arity: 1, Flags: FlagNoKey, KeySpec: KeySpec{0, 0, 0}},
	"READWRITE":        {Name: "READWRITE", Arity: 1, Flags: FlagNoKey, KeySpec: KeySpec{0, 0, 0}},
	"BROADCAST":        {Name: "BROADCAST", Arity: -2, Flags: FlagAdmin | FlagAllNodes, KeySpec: KeySpec{0, 0, 0}},
}

func GetCmdInfo(name string) *CmdInfo {
	return cmdTable[name]
}

func (c *CmdInfo) HasFlag(f CmdFlag) bool {
	return c != nil && c.Flags&f != 0
}

func (c *CmdInfo) IsWrite() bool       { return c.HasFlag(FlagWrite) }
func (c *CmdInfo) IsRead() bool        { return c.HasFlag(FlagRead) }
func (c *CmdInfo) IsMultiKey() bool    { return c.HasFlag(FlagMultiKey) }
func (c *CmdInfo) IsNoKey() bool       { return c.HasFlag(FlagNoKey) }
func (c *CmdInfo) IsBlocking() bool    { return c.HasFlag(FlagBlocking) }
func (c *CmdInfo) IsPubSub() bool      { return c.HasFlag(FlagPubSub) }
func (c *CmdInfo) IsScript() bool      { return c.HasFlag(FlagScript) }
func (c *CmdInfo) IsAllNodes() bool    { return c.HasFlag(FlagAllNodes) }
func (c *CmdInfo) IsTransaction() bool { return c.HasFlag(FlagTransaction) }
func (c *CmdInfo) IsNotAllowed() bool  { return c.HasFlag(FlagNotAllowed) }

func GetFirstKeyFromRequest(req *Request) []byte {
	if len(req.Args) == 0 {
		return nil
	}
	info := cmdTable[req.Cmd]
	if info == nil || info.KeySpec.FirstKey <= 0 || info.KeySpec.FirstKey >= len(req.Args) {
		return nil
	}
	return req.Args[info.KeySpec.FirstKey]
}

func GetFirstKeyAndInfo(req *Request) ([]byte, *CmdInfo) {
	if len(req.Args) == 0 {
		return nil, nil
	}
	info := cmdTable[req.Cmd]
	if info == nil || info.KeySpec.FirstKey <= 0 || info.KeySpec.FirstKey >= len(req.Args) {
		return nil, info
	}
	return req.Args[info.KeySpec.FirstKey], info
}

func GetKeys(req *Request) [][]byte {
	if len(req.Args) == 0 {
		return nil
	}
	info := cmdTable[req.Cmd]
	if info == nil || info.KeySpec.FirstKey == 0 {
		return nil
	}

	first := info.KeySpec.FirstKey
	last := info.KeySpec.LastKey
	step := info.KeySpec.Step
	if step <= 0 {
		step = 1
	}

	argc := len(req.Args)
	if last < 0 {
		last = argc + last
	}
	if first >= argc || last >= argc || last < first {
		return nil
	}

	var keys [][]byte
	for i := first; i <= last; i += step {
		keys = append(keys, req.Args[i])
	}
	return keys
}

// IsBlockingXREAD returns true if req is XREAD or XREADGROUP with a BLOCK argument.
func IsBlockingXREAD(req *Request) bool {
	if req.Cmd != "XREAD" && req.Cmd != "XREADGROUP" {
		return false
	}
	for i := 1; i+1 < len(req.Args); i++ {
		if bytes.EqualFold(req.Args[i], []byte("BLOCK")) {
			return true
		}
	}
	return false
}
