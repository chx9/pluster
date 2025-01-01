#ifndef _PLUSTER_CONFIG_H_
#define _PLUSTER_CONFIG_H_
#include <string.h>
#include <vector>
#include <iostream>
#include <string>
#define DEFAULT_NUM_THREADS 8
class Config {
 public:
  Config();
  std::string log_file;
  std::string config_file;
  std::vector<std::string> entries;
  int num_thread;
  int port;
};
#endif