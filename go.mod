module github.com/gtoxlili/mihomo-capture

go 1.27.3

// The dashboard's npm packages may ship Go sources of their own.
ignore ./web/node_modules

require (
	github.com/andybalholm/brotli v1.2.7
	github.com/coder/websocket v1.8.14
	github.com/dlclark/regexp2/v2 v2.11.0
	github.com/dop251/goja v0.0.0-20261029153331-f81816846776
	github.com/elazarl/goproxy v1.9.2
	github.com/go-chi/chi/v5 v5.5.0
	github.com/gobwas/ws v1.4.0
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/itchyny/gojq v0.12.19
	golang.org/x/net v0.62.0
	golang.org/x/text v0.43.0
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/gobwas/httphead v0.1.0 // indirect
	github.com/gobwas/pool v0.2.1 // indirect
	github.com/google/pprof v0.0.0-20230207041349-798e818bf904 // indirect
	github.com/itchyny/timefmt-go v0.1.8 // indirect
)
