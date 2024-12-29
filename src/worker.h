#ifndef _PLUSTER_WORKER_H_
#define _PLUSTER_WORKER_H_
#include <thread>
#include <unordered_map>
#include <vector>

#include "cluster.h"
class Worker {
 public:
  Worker();
  ~Worker();
  void run();
  void join();

 private:
  Cluster *cluster_;
};
#endif