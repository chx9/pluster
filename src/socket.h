#ifndef _PLUSTER_SOCKET_H_
#define _PLUSTER_SOCKET_H_
class Socket{
  public:
    int read();
    int write();
  private:
    int fd_;

};
#endif