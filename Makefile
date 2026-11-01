TARGET ?= m5stick-c
TINYGO ?= tinygo
COUNT ?= 8
BENCH ?= .
BENCHTIME ?= 1s

.PHONY: test check bench bench-save bench-compare alloc footprint escape profile examples

test:
	go test -race ./...

check: test alloc examples

bench:
	COUNT=$(COUNT) BENCH=$(BENCH) BENCHTIME=$(BENCHTIME) ./scripts/bench.sh run

bench-save:
	COUNT=$(COUNT) BENCH=$(BENCH) BENCHTIME=$(BENCHTIME) ./scripts/bench.sh save $(NAME)

bench-compare:
	./scripts/bench.sh compare $(FROM) $(TO)

alloc:
	./scripts/bench.sh alloc

footprint:
	go test -run TestFootprint -v

escape:
	./scripts/bench.sh escape

profile:
	./scripts/bench.sh profile

examples:
	$(TINYGO) build -target=$(TARGET) -size short -o /tmp/m5unit-nfc-read.bin ./examples/read
