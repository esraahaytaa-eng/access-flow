// Read-only API documentation: no "Try it out" (the page never calls the API) and no
// external validator badge (the document is never sent to validator.swagger.io).
window.ui = SwaggerUIBundle({
  url: "./openapi.json",
  dom_id: "#swagger-ui",
  deepLinking: true,
  supportedSubmitMethods: [],
  validatorUrl: null,
});
