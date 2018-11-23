build:
	go build

test:
	golint
	go test -cover

coverage:
	go test -coverprofile=coverage.out -test.outputdir . --
	go tool cover -html=coverage.out

fmt:
	go fmt ./...

clean:
	go clean
