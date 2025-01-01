#ifndef _PLUSTER_WORKER_H_
#define _PLUSTER_WORKER_H_
#include <thread>
#include <unordered_map>
#include <vector>

#include "cluster.h"
class Worker {
 public:
  Worker(int id);
  ~Worker();
  void run();
  int Id(){return id_;}
 private:
  int id_;
  Cluster *cluster_;
};
#endif