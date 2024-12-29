#ifndef _PLUSTER_CLUSTER_H_
#define _PLUSTER_CLUSTER_H_
#include <iostream>
#include <string>
#include <unordered_map>
#include <vector>
class Node {};

class Shard {};

class Cluster {
 public:
  Cluster();
  ~Cluster();
  bool updateCluster(std::vector<Node> nodes);

 private:
  std::unordered_map<std::string, Node> nodes_;
  std::unordered_map<std::string, Shard> shards_;
};
#endif
