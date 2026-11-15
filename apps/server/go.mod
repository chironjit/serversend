module github.com/chironjit/serversend/apps/server

go 1.26.3

require (
	github.com/boombuler/barcode v1.0.1-0.20190219062509-6c824513bacc // indirect
	github.com/chironjit/serversend/shared v0.0.0
	github.com/mattn/go-sqlite3 v1.14.53 // indirect
	github.com/pquerna/otp v1.5.0
)

replace github.com/chironjit/serversend/shared => ../../shared
