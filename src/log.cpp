
#include "log.h"
void SPDLOG::setLogLevel(const std::string &level)
{
    char L = toupper(level[0]);
    if (L == 'T') { // trace
        logger_ptr_->set_level(spdlog::level::trace);
        logger_ptr_->flush_on(spdlog::level::trace);
    }
    else if (L == 'D') { // debug
        logger_ptr_->set_level(spdlog::level::debug);
        logger_ptr_->flush_on(spdlog::level::debug);
    }
    else if (L == 'I') { // info
        logger_ptr_->set_level(spdlog::level::info);
        logger_ptr_->flush_on(spdlog::level::info);
    }
    else if (L == 'W') { // warn
        logger_ptr_->set_level(spdlog::level::warn);
        logger_ptr_->flush_on(spdlog::level::warn);
    }
    else if (L == 'E') { // error
        logger_ptr_->set_level(spdlog::level::err);
        logger_ptr_->flush_on(spdlog::level::err);
    }
    else if (L == 'C') { // critical
        logger_ptr_->set_level(spdlog::level::critical);
        logger_ptr_->flush_on(spdlog::level::critical);
    } else {
        fprintf(stderr, "Invalid log level: %s\n", level.c_str());
        exit(1);
    }
}
void SPDLOG::init(std::string log_file_path, std::string logger_name, std::string level, size_t max_file_size, size_t max_files, bool mt_security, LogDestination destination)
{
    try {
        if (destination == LogDestination::Console) {
            // Create a console logger
            auto console_sink = std::make_shared<spdlog::sinks::stdout_color_sink_mt>();
            logger_ptr_ = std::make_shared<spdlog::logger>(logger_name, console_sink);
        } else if (destination == LogDestination::File) {
            if (mt_security) {
                logger_ptr_ = spdlog::rotating_logger_mt(logger_name, log_file_path, max_file_size, max_files);
            } else {
                logger_ptr_ = spdlog::rotating_logger_st(logger_name, log_file_path, max_file_size, max_files);
            }
        }

        setLogLevel(level);
        logger_ptr_->set_pattern("[%Y-%m-%d %H:%M:%S.%e] [%l] [%s:%#] %v");
    } 
    catch (const spdlog::spdlog_ex& ex) {
        fprintf(stderr, "Log initialization failed: %s\n", ex.what());
        exit(1);
    }
}