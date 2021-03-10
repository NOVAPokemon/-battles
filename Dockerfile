FROM brunoanjos/nova-server-base:latest

ENV executable="executable"
COPY $executable .
COPY configs.json .

CMD ["sh", "-c", "./$executable"]