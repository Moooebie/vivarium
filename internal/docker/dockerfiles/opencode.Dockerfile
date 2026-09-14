FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
ENV LANG=C.UTF-8

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        bash \
        jq \
        ripgrep \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL https://opencode.ai/install | bash \
    && if [ -x /root/.opencode/bin/opencode ] && [ ! -e /usr/local/bin/opencode ]; then \
         ln -s /root/.opencode/bin/opencode /usr/local/bin/opencode; \
       fi

ENV PATH="/root/.opencode/bin:${PATH}"

WORKDIR /root
CMD ["/bin/bash"]
