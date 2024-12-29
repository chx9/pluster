#include "multiplexor.h"
#include "socket.h"
#ifndef _PLUSTER_AE_EPOLL_H_
#define _PLUSTER_AE_EPOLL_H_
class EpoolMultiplexor : public Multiplexor {
 public:
  EpoolMultiplexor();
  ~EpoolMultiplexor();
  // bool addSocket(Socket *s, int evts = ReadEvent);
};
#endif