Docker image that is a server that has a secret.

* Start registry:

`docker run -d -p 5000:5000 registry`

* Tag and push

`docker build -t 127.0.0.1:5000/test/test .`

`docker push 127.0.0.1:5000/test/test`

* Pillage the configs and search for secrets in their Configs:

`pilreg 127.0.0.1:5000 | jq .[].'Config' -r | jq . | grep SECRET`


