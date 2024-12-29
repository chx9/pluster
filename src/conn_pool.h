#ifndef _PLUSTER_CONN_POOL_H_
#define _PLUSTER_CONN_POOL_H_
#include <deque>

#include "cluster.h"
class RedisConn {};

class ConnPool {
 private:
  Node *node_;
  RedisConn shared_conn_;
  std::deque<RedisConn> private_conn_;
};

#endif