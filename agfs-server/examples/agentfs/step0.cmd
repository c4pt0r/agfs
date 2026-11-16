docker rm -f agfs
docker run -d --name agfs --device /dev/fuse --cap-add SYS_ADMIN -v "C:\Temp\rufp\check:/src" -w /src golang:1.27 sleep infinity