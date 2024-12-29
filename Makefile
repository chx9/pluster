.PHONY : default debug clean

default:
	@$(MAKE) -C src -f Makefile -j

debug:
	@$(MAKE) -C src -f Makefile debug -j

clean:
	@$(MAKE) -C src -f Makefile clean -j
