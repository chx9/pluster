#include "log.h"
int main(){
    SPDLOG::getInstance().init("log.txt", "test", "debug", 1024*1024*5, 3, true);
    LOG_INFO("Hello,%d", 1);
    
    return 0;
}