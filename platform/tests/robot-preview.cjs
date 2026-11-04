// Local synthetic-data preview only. Binds loopback and never forwards requests.
const http = require("node:http"),
  fs = require("node:fs"),
  path = require("node:path");
const { endpoint } = require("./robot-fixtures.cjs");
const staticDir = path.resolve(__dirname, "../app/static");
http
  .createServer(async (req, res) => {
    try {
      const url = new URL(req.url, "http://localhost");
      if (url.pathname.startsWith("/asrobot/api/")) {
        let text = "";
        for await (const chunk of req) text += chunk;
        const body = text ? JSON.parse(text) : {};
        res.setHeader("Content-Type", "application/json");
        res.end(
          JSON.stringify(
            endpoint(
              url.pathname.replace("/asrobot/api", "") + url.search,
              body,
            ),
          ),
        );
        return;
      }
      const name = url.pathname.includes("/static/")
          ? path.basename(url.pathname)
          : "index.html",
        file = path.join(staticDir, name);
      res.setHeader(
        "Content-Type",
        name.endsWith(".js")
          ? "text/javascript"
          : name.endsWith(".css")
            ? "text/css"
            : name.endsWith(".webp")
              ? "image/webp"
              : "text/html",
      );
      res.end(fs.readFileSync(file));
    } catch (e) {
      res.statusCode = 500;
      res.end("Preview error: " + e.message);
    }
  })
  .listen(18984, "127.0.0.1", () =>
    console.log("Synthetic preview: http://127.0.0.1:18984/asrobot/"),
  );
