#ifndef _PLUSTER_UTILS_H_
#define _PLUSTER_UTILS_H_
#include <errno.h>
#include <stdio.h>
#include <string.h>
#include <strings.h>

#include <chrono>
class StrErrorImpl {
 public:
  StrErrorImpl() { set(errno); }
  StrErrorImpl(int err) { set(err); }
  void set(int err) {
#if _GNU_SOURCE
    str_ = strerror_r(err, buf_, sizeof(buf_));
#else
    strerror_r(err, mBuf, sizeof(mBuf));
    mStr = mBuf;
#endif
  }
  const char *str() const { return str_; }

 private:
  char *str_;
  char buf_[256];
};
#define StrError(...) StrErrorImpl(__VA_ARGS__).str()

#endif