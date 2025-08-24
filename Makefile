gen:
	protoc --go_out=. --go_opt=paths=source_relative \
	--go-grpc_out=. --go-grpc_opt=paths=source_relative \
	lib/proto/*.proto

run_server:
	go run ./server -realpath ~/Desktop/Server \
	-mountpoint ~/FAT_BOY

run_client:
	go run ./client run -realpath ~/Desktop/Client -mountpoint ~/TALL_BOY \
	-email calebmwasikira@gmail.com -password password420 -remote 127.0.0.1:1054
