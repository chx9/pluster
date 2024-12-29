#ifndef _PREDIXY_TIMER_H_
#define _PREDIXY_TIMER_H_

#include <chrono>
#include <map>

#include "sync.h"

class TimerPoint {
 public:
  TimerPoint(const char *key);
  const char *key() const { return key_; }
  long elapsed() const { return elapsed_; }
  long count() const { return count_; }
  void add(long v) {
    elapsed_ += v;
    ++count_;
  }
  static void report();

 private:
  const char *key_;
  AtomicLong elapsed_;
  AtomicLong count_;
  TimerPoint *next_;

 private:
  static Mutex mtx_;
  static TimerPoint *head_;
  static TimerPoint *tail_;
  static int point_cnt_;
};

class Timer {
 public:
  Timer() : key_(nullptr), start_(now()) {}
  Timer(TimerPoint *key) : key_(key), start_(now()) {}
  ~Timer() {
    if (key_) {
      long v = elapsed();
      if (v >= 0) {
        key_->add(v);
      }
    }
  }
  void restart() { start_ = now(); }
  long stop() {
    long ret = elapsed();
    start_ = -1;
    if (ret >= 0 && key_) {
      key_->add(ret);
    }
    return ret;
  }
  long elapsed() const { return start_ < 0 ? -1 : now() - start_; }

 private:
  static long now() {
    using namespace std::chrono;
    return duration_cast<microseconds>(steady_clock::now().time_since_epoch()).count();
  }

 private:
  TimerPoint *key_;
  long start_;
};

#ifdef _PREDIXY_TIMER_STATS_

#define FuncCallTimer()                                      \
  static TimerPoint _timer_point_tttt_(__PRETTY_FUNCTION__); \
  Timer _tiemr_tttt_(&_timer_point_tttt_)

#else

#define FuncCallTimer()

#endif

#endif
