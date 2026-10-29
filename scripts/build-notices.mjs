// Preserve bundled dashboard dependencies' license and notice text.
import { readdirSync, readFileSync, realpathSync, statSync } from "node:fs"
import { join } from "node:path"
import { fileURLToPath } from "node:url"
import { execFileSync } from "node:child_process"

const store = fileURLToPath(new URL("../web/node_modules/.pnpm/", import.meta.url))
const seen = new Set()
const notices = []
function collect(packageDir) {
  const path = realpathSync(packageDir)
  if (seen.has(path)) return
  seen.add(path)
  let pkg
  try {
    pkg = JSON.parse(readFileSync(join(path, "package.json"), "utf8"))
  } catch (err) {
    if (err.code === "ENOENT") return
    throw err
  }
  const files = readdirSync(path).filter((name) => /^(licen[cs]e|copying|notice|ofl)([._-].*)?$/i.test(name) && statSync(join(path, name)).isFile())
  for (const name of files.sort()) {
    notices.push({ title: `${pkg.name}@${pkg.version} / ${name}`, text: readFileSync(join(path, name), "utf8") })
  }
}
for (const entry of readdirSync(store)) {
  const modules = join(store, entry, "node_modules")
  let names
  try { names = readdirSync(modules) } catch (err) {
    if (err.code === "ENOENT" || err.code === "ENOTDIR") continue
    throw err
  }
  for (const name of names) {
    if (name.startsWith(".")) continue
    const dir = join(modules, name)
    if (name.startsWith("@")) {
      for (const child of readdirSync(dir)) collect(join(dir, child))
    } else if (statSync(dir).isDirectory()) collect(dir)
  }
}
const modules = execFileSync("go", ["list", "-deps", "-f", "{{with .Module}}{{if not .Main}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}{{end}}", "./cmd/mihomo-capture"], { encoding: "utf8" })
const seenModules = new Set()
for (const line of modules.trim().split("\n")) {
  if (!line.trim()) continue
  const [name, version, dir] = line.split("|")
  if (seenModules.has(name)) continue
  seenModules.add(name)
  if (!dir) throw new Error(`Missing module directory for ${name}; run go mod download`)
  for (const file of readdirSync(dir).filter((file) => /^(licen[cs]e|copying|notice)([._-].*)?$/i.test(file) && statSync(join(dir, file)).isFile()).sort()) {
    notices.push({ title: `${name}@${version} / ${file}`, text: readFileSync(join(dir, file), "utf8") })
  }
}
notices.push({ title: "Go runtime and standard library / LICENSE", text: readFileSync(new URL("../licenses/Go-LICENSE", import.meta.url), "utf8") })
if (notices.length === 1) throw new Error("No dependency notices found; run make install first")
notices.sort((a, b) => a.title.localeCompare(b.title))
process.stdout.write("Third-party dependency licenses and notices\n\nIncludes Go modules and dashboard packages, including build dependencies.\n\n")
for (const notice of notices) process.stdout.write(`${notice.title}\n${"=".repeat(72)}\n${notice.text}\n\n`)
