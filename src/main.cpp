#include <cctype>
#include "log.h"
#include "config.h"
Config *config = new Config();
void init_log(){
    SPDLOG::getInstance().init("proxy.log", "test", "debug", 1024*1024*1024, 3, true, LogDestination::Console);
}
// check format to be ip:port
bool check_entry(const std::string& entry){
    for(char c : entry){
        if (!std::isdigit(c) && c != '.'  &&  c != ':'){
            return false;
        }
    }
    return true;
}

bool parseConfigFile(){
    return true;
}

void parseOptions(int argc, char* argv[]) {
    for (int i = 1; i < argc; i++) {
        if(!strcmp(argv[i], "--config") || strcmp(argv[i], "-c")){
            config->config_file = std::string(argv[++i]);
            parseConfigFile();
        } else if (!strcmp(argv[i], "--logfile")) {
            config->log_file = std::string(argv[++i]);
        } else if(!strcmp(argv[i], "--entries")){
            std::string entries = std::string(argv[++i]);
            if (entries[entries.size()-1] != ','){
                entries += ",";
            }
            std::string delimiter = ",";
            size_t pos = 0;
            std::string ep;
            while ((pos = entries.find(delimiter)) != std::string::npos) {
                ep = entries.substr(0, pos);
                if (check_entry(ep)){
                    proxyLogError("Invalid entry: {}", ep);
                    exit(1);
                }
                config->entries.push_back(ep);
                entries.erase(0, pos + delimiter.length());
            }
        } else if(!strcmp(argv[i], "--help")){
            std::cout << "Usage: " << argv[0] << " [--logfile log_file] [--config/-c config_file]" << std::endl;
            exit(0);
        }
    }
}
int main(int argc, char *argv[]){
    init_log();
    parseOptions(argc, argv);
    proxyLogInfo("log file is {}", config->log_file.c_str());
    return 0;
}