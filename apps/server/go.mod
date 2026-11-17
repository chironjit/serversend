module github.com/chironjit/serversend/apps/server

go 1.26.3

require (
	github.com/chironjit/serversend/shared v0.0.0
	github.com/pquerna/otp v1.5.0
	github.com/quic-go/quic-go v0.65.0
	golang.org/x/crypto v0.59.0
	golang.org/x/sys v0.50.0
)

require (
	github.com/boombuler/barcode v1.0.1-0.20190219062509-6c824513bacc // indirect
	github.com/mattn/go-sqlite3 v1.14.53 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/text v0.44.0 // indirect
)

replace github.com/chironjit/serversend/shared => ../../shared
