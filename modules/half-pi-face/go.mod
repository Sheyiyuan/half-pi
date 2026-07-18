module github.com/Sheyiyuan/half-pi/modules/half-pi-face

go 1.25.0

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/Sheyiyuan/half-pi/modules/gateway-core v0.0.0
)

require github.com/gorilla/websocket v1.5.3 // indirect

replace github.com/Sheyiyuan/half-pi/modules/gateway-core => ../gateway-core
