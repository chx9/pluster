#ifndef _PLUSTER_SOCKET_H_
#define _PLUSTER_SOCKET_H_
#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <netdb.h>
#include <netinet/in.h>
#include <netinet/ip.h>
#include <netinet/tcp.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <sys/uio.h>
#include <sys/un.h>
#include <unistd.h>

#include "exception.h"

class Socket {
 public:
  DefException(SocketCallFail);
  DefException(InvalidAddr);
  DefException(AddrLenTooShort);
  enum SocketType {
    RawSocket,
    ListenSocket,
    ClientSocket,
    RedisSocket,
  };
  enum StatusType {
    Normal = 0,
    None,
    End,
    IOError,
    EventError,
    ExceptError,

    CustomStatus = 100
  };

  Socket(int fd);
  Socket(int domain, int type, int protocol = 0);
  Socket(const Socket &) = delete;
  Socket(const Socket &&) = delete;
  ~Socket();
  void attach(int fd);
  void detach();
  void close();
  bool setNonBlock(bool val = true);
  bool setTcpNoDelay(bool val = true);
  bool setTcpKeepAlive(int interval);
  int read(void *buf, int cnt);
  int write(const void *buf, int cnt);
  int writev(const struct iovec *vecs, int cnt);

  int socketType() const { return type_; }
  int fd() const { return fd_; }
  bool good() const { return status_ == Normal; }
  int status() const { return status_; }
  const char *statusStr() const;
  void setStatus(int st) { status_ = st; }
  int getEvent() const { return evts_; }
  void setEvent(int evts) { evts_ = evts; }

 public:
  static int socket(int domain, int type, int protocol);
  static void getFirstAddr(const char *addr, int type, int protocol, sockaddr *res, socklen_t *len);

 protected:
  int type_;

 private:
  int fd_;
  int evts_;
  int status_;
};
#endif