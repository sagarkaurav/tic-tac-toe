FROM golang:1.24-alpine3.22 AS build
WORKDIR /code
COPY go.mod .
COPY go.sum .
RUN go mod download
COPY . .
RUN go build main.go

FROM alpine:3.22
WORKDIR /service
COPY --from=build /code/main /service/main
CMD [ "/service/main" ]