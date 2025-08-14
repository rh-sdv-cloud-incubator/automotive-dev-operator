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
      logLevel: 'debug'
    })
  );
};