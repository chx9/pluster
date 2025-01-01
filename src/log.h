#ifndef _PLUSTER_LOG_H_
#define _PLUSTER_LOG_H_
#include "exception.h"
#include <memory>
#include <string>
#include <algorithm>
#include <spdlog/spdlog.h>
#include <spdlog/sinks/rotating_file_sink.h>
#include <spdlog/sinks/stdout_color_sinks.h>
#include <assert.h>
#include "utils.h"
struct log_execption : virtual ExceptionBase {};
enum class LogDestination {
    Console,
    File
};
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
	void init(std::string log_file_path, std::string logger_name, std::string level, size_t max_file_size, size_t max_files, bool mt_security, LogDestination destination);
    std::shared_ptr<spdlog::logger> logger() { return logger_ptr_; }
}; 

#define proxyLogTrace(...) SPDLOG_LOGGER_TRACE(SPDLOG::getInstance().logger().get(), __VA_ARGS__)
#define proxyLogDebug(...) SPDLOG_LOGGER_DEBUG(SPDLOG::getInstance().logger().get(), __VA_ARGS__)
#define proxyLogInfo(...) SPDLOG_LOGGER_INFO(SPDLOG::getInstance().logger().get(), __VA_ARGS__)
#define proxyLogWarn(...) SPDLOG_LOGGER_WARN(SPDLOG::getInstance().logger().get(), __VA_ARGS__)
#define proxyLogError(...) SPDLOG_LOGGER_ERROR(SPDLOG::getInstance().logger().get(), __VA_ARGS__)
#define proxyLogCritical(...) SPDLOG_LOGGER_CRITICAL(SPDLOG::getInstance().logger().get(), __VA_ARGS__)
#endif