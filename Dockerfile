FROM golang:latest

ENV executable="executable"

RUN mkdir /service
WORKDIR /service
COPY $executable .
COPY configs.json .

COPY dockerize .
RUN chmod +x dockerize

CMD ["$executable"]