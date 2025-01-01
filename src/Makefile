CXX ?= g++
LVL ?= -g -O3
Opts += $(LVL)

LDLIBCPP=-static-libstdc++ -lspdlog 
INCLUDE_DIR=-I../deps/spdlog/include
LIBRARY_DIRS=-L../deps/spdlog/build
CXXFLAGS += $(INCLUDE_DIR) $(LIBRARY_DIRS) $(LDLIBCPP)
EV ?= auto
ifeq ($(EV), auto)
	plt = $(shell uname)
	ifeq ($(plt), Linux)
		EV = epoll
	else ifeq ($(plt), Darwin)
		EV = kqueue
		LDLIBCPP = -static-libstdc++
	else ifeq ($(plt), FreeBSD)
		EV = kqueue
		LDFLAGS += $(shell pkg info -Dx gcc|grep 'rpath') -lexecinfo
	else ifeq ($(plt), OpenBSD)
		EV = kqueue
	else
		EV = poll
	endif
endif

ifeq ($(EV), epoll)
	multiplexor = ae_epoll
	Opts += -D_EPOLL_
else ifeq ($(EV), poll)
	multiplexor = ae_poll
	Opts += -D_POLL_
else ifeq ($(EV), kqueue)
	multiplexor = ae_kqueue
	Opts += -D_KQUEUE_
else
$(error Unknown event:$(EV))
endif

target = pluster
objs = \
	   socket.o \
	   $(multiplexor).o \
	   ae_epoll.o \
	   ae_poll.o \
	   ae_kqueue.o \
	   log.o \
	   main.o \

default: $(target)
$(target): $(objs)
	$(CXX) $(CFLAGS) $(CXXFLAGS) -o $@ $^ $(LDFLAGS)