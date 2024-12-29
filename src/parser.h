#ifndef _PLUSTER_PARSER_H_
#define _PLUSTER_PARSER_H_
#include <string>
class ClusterNodesParser {
 public:
  ClusterNodesParser();
  ~ClusterNodesParser();
  bool parse(std::string nodes);

 private:
  std::string nodes_;
};
#endif