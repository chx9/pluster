#ifndef _PLUSTER_MULTIPLEXOR_H_
#define _PLUSTER_MULTIPLEXOR_H_
class Multiplexor {
 public:
  virtual ~Multiplexor();

 protected:
  Multiplexor();
};
#ifdef _KQUEUE_
#include "ae_kqueue.h"
#elif _EPOLL_
#include "ae_epoll.h"
#elif _POLL_
#include "ae_poll.h"
#endif
#endif