#include <sys/types.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <sys/uio.h>
#include <netinet/in.h>
#include <netinet/ip.h>
#include <netinet/tcp.h>
#include <fcntl.h>
#include <string.h>
#include <string>
#include "socket.h"
#include "utils.h"
#include "timer.h"

Socket::Socket(int fd):
    type_(RawSocket),
    fd_(fd),
    evts_(0),
    status_(fd >= 0 ? Normal : None)
{
}

Socket::Socket(int domain, int type, int protocol):
    type_(RawSocket),
    fd_(Socket::socket(domain, type, protocol)),
    status_(Normal)
{
}

Socket::~Socket()
{
    close();
}

void Socket::close()
{
    if (fd_ >= 0) {
        ::close(fd_);
        fd_ = -1;
        evts_ = 0;
        status_ = None;
    }
}

void Socket::attach(int fd)
{
    close();
    fd_ = fd;
    evts_ = 0;
    status_ = fd >= 0 ? Normal : None;
}

void Socket::detach()
{
    fd_ = -1;
    evts_ = 0;
    status_ = None;
}

const char* Socket::statusStr() const
{
    static const char* strs[] = {
        "Normal",
        "None",
        "End",
        "IOError",
        "EventError",
        "ExceptError"
    };
    return status_ < CustomStatus ? strs[status_] : "Custom";
}

int Socket::socket(int domain, int type, int protocol)
{
    int fd = ::socket(domain, type, protocol);
    if (fd < 0) {
        Throw(SocketCallFail, "%s", StrError());
    }
    return fd;
}

void Socket::getFirstAddr(const char* addr, int type, int protocol, sockaddr* res, socklen_t* len)
{
    if (*addr == '/') { //unix socket
        struct sockaddr_un sun;
        memset(&sun, 0, sizeof(sun));
        sun.sun_family = AF_UNIX;
        strncpy(sun.sun_path, addr, sizeof(sun.sun_path));
        if (*len < sizeof(sun)) {
            *len = sizeof(sun);
            Throw(AddrLenTooShort, "result address space too short");
        }
        *len = sizeof(sun);
        memcpy(res, &sun, *len);
    } else {
        std::string tmp;
        const char* host = addr;
        const char* port = strrchr(addr, ':');
        if (port) {
            tmp.append(addr, port - addr);
            host = tmp.c_str();
            port++;
        }
        struct addrinfo hints;
        struct addrinfo *dst;
        memset(&hints, 0, sizeof(hints));
        hints.ai_family = AF_UNSPEC;
        hints.ai_socktype = type;
        hints.ai_flags = AI_PASSIVE;
        hints.ai_protocol = protocol;
        int s = getaddrinfo(host, port, &hints, &dst);
        if (s != 0) {
            Throw(InvalidAddr, "invalid addr %s:%s", addr, gai_strerror(s));
        }
        if (!dst) {
            Throw(InvalidAddr, "invalid addr %s", addr);
        }
        if (*len < dst->ai_addrlen) {
            *len = dst->ai_addrlen;
            Throw(AddrLenTooShort, "result address space too short");
        }
        *len = dst->ai_addrlen;
        memcpy(res, dst->ai_addr, *len);
        freeaddrinfo(dst);
    }
}

bool Socket::setNonBlock(bool val)
{
    int flags = fcntl(fd_, F_GETFL, NULL);
    if (flags < 0) {
        return false;
    }
    if (val) {
        flags |= O_NONBLOCK;
    } else {
        flags &= ~O_NONBLOCK;
    }
    int ret = fcntl(fd_, F_SETFL, flags);
    return ret == 0;
}

bool Socket::setTcpNoDelay(bool val)
{
    int nodelay = val ? 1 : 0;
    socklen_t len = sizeof(nodelay);
    int ret = setsockopt(fd_, IPPROTO_TCP, TCP_NODELAY, &nodelay, len);
    return ret == 0;
}

bool Socket::setTcpKeepAlive(int interval)
{
    int val = 1;
    int ret = setsockopt(fd_, SOL_SOCKET, SO_KEEPALIVE, &val, sizeof(val));
    if (ret != 0) {
        return false;
    }
#ifdef __linux__
    val = interval;
    ret = setsockopt(fd_, IPPROTO_TCP, TCP_KEEPIDLE, &val, sizeof(val));
    if (ret != 0) {
        return false;
    }
    val = interval / 3;
    ret = setsockopt(fd_, IPPROTO_TCP, TCP_KEEPINTVL, &val, sizeof(val));
    if (ret != 0) {
        return false;
    }
    val = 3;
    ret = setsockopt(fd_, IPPROTO_TCP, TCP_KEEPCNT, &val, sizeof(val));
    if (ret != 0) {
        return false;
    }
#else
    ((void)interval); //Avoid unused var warning for non Linux systems
#endif
    return true;
}

int Socket::read(void* buf, int cnt)
{
    FuncCallTimer();
    while (cnt > 0) {
        int n = ::read(fd_, buf, cnt);
        if (n < 0) {
            if (errno == EAGAIN || errno == EWOULDBLOCK) {
                break;
            } else if (errno == EINTR) {
                continue;
            }
            status_ = IOError;
        } else if (n == 0) {
            status_ = End;
        }
        return n;
    }
    return 0;
}

int Socket::write(const void* buf, int cnt)
{
    FuncCallTimer();
    while (cnt > 0) {
        int n = ::write(fd_, buf, cnt);
        if (n < 0) {
            if (errno == EAGAIN || errno == EWOULDBLOCK) {
                break;
            } else if (errno == EINTR) {
                continue;
            }
            status_ = IOError;
        }
        return n;
    }
    return 0;
}

int Socket::writev(const struct iovec* vecs, int cnt)
{
    FuncCallTimer();
    while (cnt > 0) {
        int n = ::writev(fd_, vecs, cnt);
        if (n < 0) {
            if (errno == EAGAIN || errno == EWOULDBLOCK) {
                return 0;
            } else if (errno == EINTR) {
                continue;
            }
            status_ = IOError;
        }
        return n;
    }
    return 0;
}