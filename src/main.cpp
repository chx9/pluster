#include "log.h"
#include "config.h"
Config *config = new Config();
void init_log(){
    SPDLOG::getInstance().init("proxy.log", "test", "debug", 1024*1024*1024, 3, true, LogDestination::File);
}
void parse_options(int argc, char* argv[]) {
    for (int i = 1; i < argc; i++) {
        if (strcmp(argv[i], "--logfile") == 0) {
                config->log_file = std::string(argv[++i]);
            } 
        }
}
int main(int argc, char *argv[]){
    init_log();
    parse_options(argc, argv);
    proxyLogInfo("log file is {}", config->log_file.c_str());
    proxyLogInfo("sdfsd");
    return 0;
}