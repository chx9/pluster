#include <cctype>
#include <thread>
#include <vector>
#include "log.h"
#include "config.h"
#include "worker.h"
Config *config = new Config();
void initLog(){
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
        if(!strcmp(argv[i], "--config") || !strcmp(argv[i], "-c")){
            config->config_file = std::string(argv[++i]);
            parseConfigFile();
        } else if (!strcmp(argv[i], "--port") || !strcmp(argv[i], "-p")) {
            try{
                config->port = std::stoi(argv[++i]);
            }catch(std::exception &e){
                proxyLogError("Invalid port number:{}", argv[i]);
                exit(1);
            }
        } else if (!strcmp(argv[i], "--num_thread") || !strcmp(argv[i], "-n")) {
            config->num_thread = std::stoi(argv[++i]);  
        }
        else if (!strcmp(argv[i], "--logfile")) {
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
                proxyLogInfo("entires", ep);
                if (!check_entry(ep)){
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
void initListener(){
    
}
int main(int argc, char *argv[]){
    initLog();
    initListener();
    parseOptions(argc, argv);
    std::vector<std::thread> threads;
    std::vector<Worker*> workers;
    for(int i=0;i<config->num_thread;i++){
        Worker *worker = new Worker(i); 
        workers.emplace_back(worker);
        threads.emplace_back(std::thread(&Worker::run, worker));
    }
    for(auto &t: threads){
        t.join();
    }
    return 0;
}