BINARY=corsproxyd

.DEFAULT_GOAL: $(BINARY)

$(BINARY):
	go build -o ${BINARY} main.go

install:
	go install ./...

build: ${BINARY}

clean:
		if [ -f ${BINARY} ] ; then rm ${BINARY} ; fi

all:
	build
