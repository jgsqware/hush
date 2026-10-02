# Image published by goreleaser: hush plus ansible-core, ready to run playbooks.
#   docker run --rm -v "$PWD:/work" ghcr.io/jgsqware/hush -- ansible-playbook site.yml -i inventory
FROM python:3.13-slim

ARG ANSIBLE_CORE=2.21.*
RUN apt-get update \
 && apt-get install -y --no-install-recommends openssh-client sshpass \
 && rm -rf /var/lib/apt/lists/* \
 && pip install --no-cache-dir "ansible-core==${ANSIBLE_CORE}"

ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/hush /usr/local/bin/hush

WORKDIR /work
ENTRYPOINT ["hush"]
CMD ["--help"]
