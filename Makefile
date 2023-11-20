install:
	go install ./cometmock

test-locally:
	go test -timeout 600s ./e2e-tests -test.v 

test-docker:
	# Build the Docker image
	docker build -f Dockerfile.test -t cometmock-test .

	# Start a container and execute the test command inside
	docker rm cometmock-test-instance || true
	docker run --name cometmock-test-instance --workdir /CometMock cometmock-test go test -timeout 600s ./e2e-tests -test.v; sleep 500000000