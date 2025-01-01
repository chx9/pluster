#ifndef _PLUSTER_LOG_H_
#define _PLUSTER_LOG_H_
#include "exception.h"
#include <memory>
#include <string>
#include <algorithm>
#include <spdlog/spdlog.h>
#include <spdlog/sinks/rotating_file_sink.h>
#include <assert.h>
#include "utils.h"
struct log_execption : virtual ExceptionBase {};

class SPDLOG {
private:
    SPDLOG() = default;
private:
    std::shared_ptr<spdlog::logger> logger_ptr_;
    void setLogLevel(const std::string& level);

public:
	static SPDLOG& getInstance() {
		static SPDLOG instance;
		return instance;
	}
    // 初始化一个默认日志文件logger: 日志路径；logger name; 日志等级；单个日志文件最大大小；回滚日志文件个数；日志是否线程安全；
	void init(std::string log_file_path,std::string logger_name, std::string level, size_t max_file_size, size_t max_files, bool mt_security = true); 
    std::shared_ptr<spdlog::logger> logger() { return logger_ptr_; }
}; // SPDLOG class

#define LOG_TRACE(...)       SPDLOG::getInstance().logger().get()->trace(__VA_ARGS__)
#define LOG_DEBUG(...)       SPDLOG::getInstance().logger().get()->debug(__VA_ARGS__)
#define LOG_INFO(...)        SPDLOG::getInstance().logger().get()->info(__VA_ARGS__)
#define LOG_WARN(...)        SPDLOG::getInstance().logger().get()->warn(__VA_ARGS__)
#define LOG_ERROR(...)       SPDLOG::getInstance().logger().get()->error(__VA_ARGS__)
#define LOG_CRITICAL(...)    SPDLOG::getInstance().logger().get()->critical(__VA_ARGS__)
#endif