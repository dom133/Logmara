# RabbitMQ config for docker-stack.rabbitmq.yml / docker-compose.
# Default user + password. The `logmara` user is created with no
# loopback restriction, so it can connect from any host.

default_user = logmara
default_pass = __RABBITMQ_PASSWORD__
