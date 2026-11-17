import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { chooseVersion } from "./release.mjs";

test("increments the highest unified or historical component version numerically", () => {
  assert.equal(chooseVersion([]), "0.1.0");
  assert.equal(chooseVersion(["v0.1.2", "server/v0.1.9", "desktop/v0.1.3"]), "0.1.10");
  assert.equal(chooseVersion(["v0.9.0", "v0.10.2", "v1.0.0-beta"]), "0.10.3");
  assert.equal(chooseVersion(["v0.1.4"], "0.2.0"), "0.2.0");
  for (const value of ["0.1.4", "0.1.3", "v0.2.0", "01.2.3", "0.2.0-rc1", "$(oops)"]) {
    assert.throws(() => chooseVersion(["server/v0.1.4"], value));
  }
});

test("release preflight and tag push against an isolated local origin", () => {
  const directory = mkdtempSync(join(tmpdir(), "serversend-release-"));
  const remote = join(directory, "origin.git");
  const work = join(directory, "work");
  const git = (...args) =>
    execFileSync("git", args, {
      cwd: directory,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
  const childEnv = { ...process.env };
  delete childEnv.NODE_TEST_CONTEXT;
  const run = (env = {}) =>
    spawnSync(process.execPath, [fileURLToPath(new URL("./release.mjs", import.meta.url))], {
      cwd: work,
      encoding: "utf8",
      env: { ...childEnv, VERSION: "", DRY_RUN: "", ...env },
    });
  try {
    git("init", "--bare", "--initial-branch=main", remote);
    git("init", "--initial-branch=main", work);
    git("-C", work, "config", "user.email", "release@example.invalid");
    git("-C", work, "config", "user.name", "Release Test");
    git("-C", work, "commit", "--allow-empty", "-m", "Initial");
    git("-C", work, "remote", "add", "origin", remote);
    git("-C", work, "tag", "server/v0.1.4");
    git("-C", work, "push", "-u", "origin", "main", "--tags");
    let result = run({ DRY_RUN: "1" });
    assert.equal(result.status, 0, JSON.stringify(result));
    assert.match(result.stdout, /Release v0.1.5/);
    assert.equal(git("-C", work, "tag", "--list", "v*"), "");
    assert.equal(run({ VERSION: "0.1.4", DRY_RUN: "1" }).status, 1);
    assert.match(run({ DRY_RUN: "true" }).stderr, /DRY_RUN must be/);
    writeFileSync(join(work, "untracked"), "dirty");
    assert.match(run().stderr, /working-tree/);
    rmSync(join(work, "untracked"));
    git("-C", work, "checkout", "-b", "feature");
    assert.match(run().stderr, /default branch/);
    git("-C", work, "checkout", "main");
    git("-C", work, "commit", "--allow-empty", "-m", "Not pushed");
    assert.match(run().stderr, /default branch/);
    git("-C", work, "push", "origin", "main");
    result = run();
    assert.equal(result.status, 0, JSON.stringify(result));
    assert.match(git("--git-dir", remote, "tag", "--list"), /v0.1.5/);
    assert.equal(git("-C", work, "cat-file", "-t", "v0.1.5"), "tag");
    assert.equal(git("-C", work, "rev-parse", "v0.1.5^{}"), git("-C", work, "rev-parse", "HEAD"));
    const validation = spawnSync(
      process.execPath,
      [fileURLToPath(new URL("./release.mjs", import.meta.url)), "--validate-tag", "v0.1.5"],
      { cwd: work, encoding: "utf8", env: childEnv },
    );
    assert.equal(validation.status, 0, validation.stderr);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
