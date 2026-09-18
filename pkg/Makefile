# ============================================================================
# pkg —— 跨服务协议模块（仅 .proto 与生成的 .pb.go，禁止任何工具代码）
# ============================================================================

PROTOC      ?= protoc
PROTO_FILES := \
	account/v1/account.proto \
	chat/v1/conversation.proto \
	chat/v1/message.proto \
	chat/v1/group.proto \
	chat/v1/friend.proto

.PHONY: proto
proto:
	@$(PROTOC) \
		--proto_path=. \
		--go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		$(PROTO_FILES)
	@echo ">> proto generated"

.PHONY: clean
clean:
	@rm -f $(PROTO_FILES:.proto=.pb.go) $(PROTO_FILES:.proto=_grpc.pb.go)
	@echo ">> proto cleaned"
