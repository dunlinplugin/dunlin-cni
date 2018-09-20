FROM ubuntu:16.04

MAINTAINER "LeanNet" <info@leannet.eu>
# Init container for the LeanNet Kubernetes Plugin 

RUN apt-get update
RUN apt-get install -y openvswitch-switch

COPY init-node.sh ./
RUN chmod 755 init-node.sh

COPY dunlin-cni ./dunlin
RUN chmod 755 dunlin

ENTRYPOINT ["sh", "-c", "./init-node.sh"] 

