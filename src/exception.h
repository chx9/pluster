#ifndef _PLUSTER_EXCEPTION_H_
#include <stdarg.h>
#include <stdio.h>

#include <exception>
class ExceptionBase : public std::exception {
 public:
  static const int MaxMsgLen = 1024;

 public:
  ExceptionBase() { msg_[0] = '\0'; }
  ExceptionBase(const char *file, int line, const char *fmt, ...) {
    int n = snprintf(msg_, sizeof(msg_), "%s:%d ", file, line);
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(msg_ + n, sizeof(msg_) - n, fmt, ap);
    va_end(ap);
  }
  ExceptionBase(const char *fmt, ...) {
    va_list ap;
    va_start(ap, fmt);
    vsnprintf(msg_, sizeof(msg_), fmt, ap);
    va_end(ap);
  }
  ~ExceptionBase() {}
  const char *what() const noexcept { return msg_; }

 protected:
  void init(const char *fmt, va_list ap) { vsnprintf(msg_, sizeof(msg_), fmt, ap); }

 private:
  char msg_[MaxMsgLen];
};

#define DefException(T)                        \
  class T : public ExceptionBase {             \
   public:                                     \
    template <class... A>                      \
    T(A &&...args) : ExceptionBase(args...) {} \
  }

#define Throw(T, ...) throw T(__FILE__, __LINE__, ##__VA_ARGS__)

#endif
