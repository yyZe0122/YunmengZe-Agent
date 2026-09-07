const esbuild = require("esbuild")
const fs = require("fs")
const path = require("path")

const production = process.argv.includes("--production")
const watch = process.argv.includes("--watch")

const common = {
  bundle: true,
  minify: production,
  sourcemap: !production,
  sourcesContent: false,
  logLevel: "info",
}

async function main() {
  const ext = await esbuild.context({
    ...common,
    entryPoints: ["src/extension.ts"],
    format: "cjs",
    platform: "node",
    outfile: "dist/extension.js",
    external: ["vscode"],
  })
  const web = await esbuild.context({
    ...common,
    entryPoints: ["src/webview/main.ts"],
    format: "iife",
    platform: "browser",
    outfile: "dist/webview.js",
    target: "es2022",
  })
  const copyCss = () => {
    fs.mkdirSync("dist", { recursive: true })
    fs.copyFileSync(path.join("src", "webview", "styles.css"), path.join("dist", "webview.css"))
  }
  if (watch) {
    copyCss()
    await Promise.all([ext.watch(), web.watch()])
    fs.watch(path.join("src", "webview", "styles.css"), copyCss)
  } else {
    await ext.rebuild()
    await web.rebuild()
    await ext.dispose()
    await web.dispose()
    copyCss()
  }
}

main().catch((err) => {
  console.error(err)
  process.exit(1)
})
