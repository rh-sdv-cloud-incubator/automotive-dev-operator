const { createProxyMiddleware } = require('http-proxy-middleware');

module.exports = function(app) {
  app.use(
    '/v1',
    createProxyMiddleware({
      target: 'https://ado-build-api-automotive-dev-operator-system.apps.rosa.auto-devcluster.bzdx.p3.openshiftapps.com',
      changeOrigin: true,
      secure: true,
      logLevel: 'debug'
    })
  );
};