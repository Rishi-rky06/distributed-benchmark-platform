FROM nginx:alpine AS development

COPY . /usr/share/nginx/html/

# Simple nginx config that proxies API calls to backend
RUN echo 'server { \
    listen 5173; \
    root /usr/share/nginx/html; \
    index index.html; \
    location /api/ { proxy_pass http://backend:8080; } \
    location /health { proxy_pass http://backend:8080; } \
    location /readiness { proxy_pass http://backend:8080; } \
}' > /etc/nginx/conf.d/default.conf

EXPOSE 5173
CMD ["nginx", "-g", "daemon off;"]
