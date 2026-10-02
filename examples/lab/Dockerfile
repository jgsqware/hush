FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends openssh-server python3 sudo \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /run/sshd \
 && useradd -m -s /bin/bash hush \
 && echo 'hush ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/hush
COPY --chown=hush:hush id_ed25519.pub /home/hush/.ssh/authorized_keys
RUN chmod 700 /home/hush/.ssh && chmod 600 /home/hush/.ssh/authorized_keys
EXPOSE 22
CMD ["/usr/sbin/sshd", "-D", "-e"]
