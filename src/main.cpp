#include "log.h"
int main(){
    SPDLOG::getInstance().init("proxy.log", "test", "debug", 1024*1024*5, 3, true, LogDestination::Console);
    proxyLogInfo("Hello,{}", "sdf");
    
    return 0;
}