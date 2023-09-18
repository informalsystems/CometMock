install:
	go install ./cometmock

test-locally:
	go test -timeout 600s ./e2e-tests -test.v 

test-docker:
	# Build the Docker image
	docker build -f Dockerfile-test -t cometmock-test .

	docker rm cometmock-test-instance || true
	docker run --name cometmock-test-instance --workdir /CometMock cometmock-test ls -l /usr/bin/simd
	
	# Start a container and execute the test command inside
	docker run --name cometmock-test-instance --workdir /CometMock cometmock-test go test -timeout 600s ./e2e-tests -test.v
	
	# Remove the container after tests
	docker rm cometmock-test-instance