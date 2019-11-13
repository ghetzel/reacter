FROM ubuntu:bionic
MAINTAINER its@gary.cool
COPY bin/reacter-linux-amd64 /usr/bin/reacter
EXPOSE 6773/tcp

RUN apt-get update
RUN apt-get install -y libsass0
RUN apt-get clean

CMD ["/usr/bin/reacter", "--http-address", ":6773", "--config-dir", "/config", "check"]
