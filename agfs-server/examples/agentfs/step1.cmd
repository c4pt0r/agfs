:docker exec agfs -- sh -c "apt-get update -qq >/dev/null 2>&1; echo update-rc=$?; apt-get install -y -qq fuse3 >/dev/null 2>&1; echo install-rc=$?; which fusermount3 fusermount; ls -la /dev/fuse 2>&1"
docker exec agfs sh -c "apt-get update -qq && apt-get install -y -qq fuse3 && fusermount3 --version"
