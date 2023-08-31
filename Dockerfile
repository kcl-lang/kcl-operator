FROM golang:1.19 as builder

ENV GO111MODULE=on \
    GOPROXY=https://goproxy.cn,direct

WORKDIR /

COPY . .

RUN GOOS=linux GOARCH=amd64 go build -o manager

FROM kcllang/kcl

WORKDIR /
COPY --from=builder /manager .

ENV KCL_GO_DISABLE_ARTIFACT=on
ENV LANG="en_US.UTF-8"

ENTRYPOINT ["/manager"]
