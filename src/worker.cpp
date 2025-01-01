#include "worker.h"
#include "log.h"
Worker::Worker(int id):id_(id){

}
void Worker::run(){
   while(true){
       std::this_thread::sleep_for(std::chrono::seconds(1));
       proxyLogInfo("Worker {} is running", id_);
   }
}