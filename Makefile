gen:
	protoc -I proto/ --go_out=. proto/*.proto

run:
	go run .
