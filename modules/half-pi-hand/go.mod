module github.com/Sheyiyuan/half-pi/modules/half-pi-hand

go 1.25.0

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/Sheyiyuan/half-pi/modules/gateway-core v0.0.0
	github.com/Sheyiyuan/half-pi/modules/half-pi-core v0.0.0
	github.com/gorilla/websocket v1.5.3
)

replace (
	github.com/Sheyiyuan/half-pi/modules/gateway-core => ../gateway-core
	github.com/Sheyiyuan/half-pi/modules/half-pi-core => ../half-pi-core
)
