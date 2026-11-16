docker exec -d agfs sh -c "cd /src && ./agfs-server/agfs-server-linux -c config-agentfs.docker.yaml > /tmp/server.log 2>&1"
docker exec agfs sh -c "sleep 1 && tail -5 /tmp/server.log"