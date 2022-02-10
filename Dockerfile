##################################
# STEP 1 build executable binary #
##################################
FROM golang:1.16-alpine AS builder

# Install git. Git is required for fetching the dependencies.
RUN apk update && apk add --no-cache git

# Update certs.
RUN apk add --update ca-certificates

ENV APP_HOME /go/src/github.com/NobleD5/alertmanager-bot
WORKDIR $APP_HOME

# Copy src.
COPY  /cmd/alertmanager-bot     $APP_HOME/cmd/alertmanager-bot
COPY  /pkg/alertmanager         $APP_HOME/pkg/alertmanager
COPY  /pkg/translation          $APP_HOME/pkg/translation
COPY  /pkg/telegram             $APP_HOME/pkg/telegram
COPY  /pkg/vendor               $APP_HOME/pkg/vendor

COPY  /go.mod                   $APP_HOME/

# Fetch dependencies using go get.
RUN go get -d -v                $APP_HOME/cmd/alertmanager-bot

ARG ver="0.0.1"
ARG branch="HEAD"
ARG hash="hash"
ARG user="NobleD5"
ARG date="20060102-15:04:05"

ENV VERSION   "-X github.com/prometheus/common/version.Version=$ver"
ENV BRANCH    "-X github.com/prometheus/common/version.Branch=$branch"
ENV REVISION  "-X github.com/prometheus/common/version.Revision=$hash"
ENV USER      "-X github.com/prometheus/common/version.BuildUser=$user"
ENV DATE      "-X github.com/prometheus/common/version.BuildDate=$date"

# Build the binary.
RUN GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build \
  -ldflags="-w -s $VERSION $BRANCH $REVISION $USER $DATE" \
  -o /go/bin/main $APP_HOME/cmd/alertmanager-bot/

##############################
# STEP 2 build a small image #
##############################
FROM scratch

LABEL description="Scratch-based Docker image for alertmanager-bot"

# Import the user and group files from the builder.
COPY --from=builder /etc/passwd     /etc/passwd

# Import certs from the builder.
COPY --from=builder /etc/ssl/certs  /etc/ssl/certs

# Copy static executable.
COPY --from=builder /go/bin/main    /bin/alertmanager-bot

# Copy default template.
COPY                default.tmpl    /templates/default.tmpl

# Copy default lang yaml.
COPY                en.yaml         /dicts/en.yaml

VOLUME     [ "/alertmanager-bot" ]
WORKDIR     /alertmanager-bot

# Run binary.
ENTRYPOINT [ "/bin/alertmanager-bot" ]
