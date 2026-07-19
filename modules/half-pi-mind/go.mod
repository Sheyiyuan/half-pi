module github.com/Sheyiyuan/half-pi/modules/half-pi-mind

go 1.25.0

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/Microsoft/go-winio v0.6.2
	github.com/Sheyiyuan/half-pi/modules/gateway-core v0.0.0-20260714110534-880fe4d6f2ab
	github.com/Sheyiyuan/half-pi/modules/half-pi-core v0.0.0
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/mattn/go-isatty v0.0.20
	golang.org/x/sys v0.44.0
	modernc.org/sqlite v1.53.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	modernc.org/libc v1.73.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace github.com/Sheyiyuan/half-pi/modules/half-pi-core => ../half-pi-core

replace github.com/Sheyiyuan/half-pi/modules/gateway-core => ../gateway-core
