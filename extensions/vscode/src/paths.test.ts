import assert from "node:assert/strict"
import test from "node:test"
import path from "node:path"
import {
  composeMessage,
  expandChatCommandTemplate,
  isBuiltinSlash,
  normalizeDecision,
  parseCronEvery,
  parseSlash,
  taskTitle,
  withLineRange,
  workspaceRelative,
} from "./paths"

test("workspace relative inside root", () => {
  const root = path.sep === "/" ? "/home/yyze/proj" : "C:\\home\\yyze\\proj"
  const file = path.join(root, "src", "a.ts")
  const ref = workspaceRelative(file, root)
  assert.equal(ref.outside, false)
  assert.equal(ref.mention, "@src/a.ts")
})

test("workspace outside uses absolute mention", () => {
  const root = path.sep === "/" ? "/home/yyze/proj" : "C:\\home\\yyze\\proj"
  const file = path.sep === "/" ? "/tmp/shot.png" : "D:\\shot.png"
  const ref = workspaceRelative(file, root)
  assert.equal(ref.outside, true)
  assert.ok(ref.mention.startsWith("@"))
  assert.ok(ref.mention.includes("shot.png"))
  assert.ok(!ref.mention.startsWith("@../"))
})

test("line range", () => {
  assert.equal(withLineRange("@a.ts", 3, 3), "@a.ts#L3")
  assert.equal(withLineRange("@a.ts", 3, 9), "@a.ts#L3-9")
})

test("compose message keeps chips off the typed line", () => {
  assert.equal(composeMessage("look", ["@src/a.ts", "@/tmp/x.png"]), "look\n\n@src/a.ts @/tmp/x.png")
  assert.equal(composeMessage("  ", ["@a"]), "@a")
})

test("slash parse and builtins", () => {
  assert.deepEqual(parseSlash("/compact focus"), { name: "compact", arg: "focus" })
  assert.equal(parseSlash("hello"), undefined)
  assert.equal(isBuiltinSlash("perm"), true)
  assert.equal(isBuiltinSlash("my-skill"), false)
})

test("command template", () => {
  assert.equal(expandChatCommandTemplate("Review $ARGUMENTS", "auth"), "Review auth")
  assert.equal(expandChatCommandTemplate("Ping", "x"), "Ping\n\nx")
})

test("decision aliases", () => {
  assert.equal(normalizeDecision("once"), "allow_once")
  assert.equal(normalizeDecision("permanent"), "allow_permanent")
  assert.equal(normalizeDecision("nope"), undefined)
})

test("cron every matches Go duration", () => {
  assert.equal(parseCronEvery("15m"), 900)
  assert.equal(parseCronEvery("1h30m"), 5400)
  assert.equal(parseCronEvery("1.5h"), 5400)
  assert.throws(() => parseCronEvery("15"))
  assert.throws(() => parseCronEvery("1H"))
  assert.throws(() => parseCronEvery("500ms"))
})

test("task title ellipsis", () => {
  const long = "x".repeat(90)
  assert.equal(taskTitle(long).endsWith("…"), true)
  assert.equal(taskTitle("short"), "short")
})
