module github.com/NobleD5/alertmanager-bot

go 1.16

require (
	github.com/boltdb/bolt v1.3.1 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible
	github.com/dchest/uniuri v0.0.0-20200228104902-7aecb25e1fe5
	github.com/docker/libkv v0.2.1
	github.com/go-kit/kit v0.10.0
	github.com/hako/durafmt v0.0.0-20200710122514-c0fb7b4da026
	github.com/joho/godotenv v1.4.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/alertmanager v0.21.1-0.20210129135951-f2f7a72813af
	github.com/prometheus/client_golang v1.11.1
	github.com/prometheus/common v0.26.0
	github.com/stretchr/testify v1.7.0
	golang.org/x/text v0.3.8
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
	gopkg.in/tucnak/telebot.v2 v2.3.5
	gopkg.in/yaml.v2 v2.4.0
)
