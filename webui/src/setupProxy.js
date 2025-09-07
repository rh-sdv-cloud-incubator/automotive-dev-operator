const { createProxyMiddleware } = require('http-proxy-middleware');

module.exports = function(app) {
  const defaultTarget = 'http://localhost:8080';
  const target = process.env.WEBUI_PROXY_TARGET || process.env.REACT_APP_WEBUI_PROXY_TARGET || defaultTarget;
  const secure = process.env.WEBUI_PROXY_SECURE === 'false' ? false : true;

  app.use(
    '/v1',
    createProxyMiddleware({
      target,
      changeOrigin: true,
      secure,
      logLevel: 'debug',
      onProxyReq: (proxyReq) => {
        const token = process.env.DEV_BEARER_TOKEN || process.env.REACT_APP_DEV_BEARER_TOKEN;
        if (token) proxyReq.setHeader('Authorization', `Bearer ${token}`);
      }
    })
  );
};