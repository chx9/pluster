#include "config.h"

Config::Config(){
    log_file = "";
    config_file = "";
    entries = {};
    num_thread = DEFAULT_NUM_THREADS; 
}