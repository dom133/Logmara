# RabbitMQ config for docker-stack.rabbitmq.yml / docker-compose.
# Default user + password. loopback_users = [] so the `logmara` user
# can connect from any host (HAProxy, api containers, etc.).

default_user = logmara
default_pass = __RABBITMQ_PASSWORD__
loopback_users = []
