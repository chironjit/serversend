import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";

const pattern = /^(?:v|server\/v|desktop\/v)(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
export function versions(tags) {
  return tags
    .map((tag) => pattern.exec(tag))
    .filter(Boolean)
    .map((match) => match.slice(1).map(BigInt))
    .sort((a, b) => {
      for (let i = 0; i < 3; i++) if (a[i] !== b[i]) return a[i] < b[i] ? -1 : 1;
      return 0;
    });
}
export function chooseVersion(tags, requested = "") {
  const latest = versions(tags).at(-1);
  const next = requested || (latest ? `${latest[0]}.${latest[1]}.${latest[2] + 1n}` : "0.1.0");
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(next)) {
    throw new Error("VERSION must be X.Y.Z without a prefix or prerelease suffix.");
  }
  if (latest) {
    const candidate = versions([`v${next}`])[0];
    const difference = candidate.findIndex((value, i) => value !== latest[i]);
    if (difference === -1 || candidate[difference] < latest[difference]) {
      throw new Error(
        `Version must exceed ${latest.join(".")}, including historical component tags.`,
      );
    }
  }
  return next;
}

function main() {
  const git = (...args) => execFileSync("git", args, { encoding: "utf8" }).trim();
  const tags = () => git("tag", "--list").split("\n");
  if (process.argv[2] === "--validate-tag") {
    const tag = process.argv[3] || "";
    if (!/^v\d+\.\d+\.\d+$/.test(tag)) throw new Error("Expected a unified vX.Y.Z tag.");
    chooseVersion(
      tags().filter((value) => value !== tag),
      tag.slice(1),
    );
    console.log(tag.slice(1));
    return;
  }
  if (process.argv.length > 2) throw new Error("Use make release [VERSION=X.Y.Z] [DRY_RUN=1].");
  if (![undefined, "", "0", "1"].includes(process.env.DRY_RUN)) {
    throw new Error("DRY_RUN must be 1 (preview) or 0 (publish).");
  }
  if (git("status", "--porcelain"))
    throw new Error("Commit or remove working-tree changes before releasing.");
  git("symbolic-ref", "--quiet", "--short", "HEAD");
  git("fetch", "origin", "--tags");
  const remoteHead = git("ls-remote", "--symref", "origin", "HEAD");
  const branch = /^ref: refs\/heads\/(.+)\tHEAD$/m.exec(remoteHead)?.[1];
  const remoteCommit = /^([a-f0-9]+)\tHEAD$/m.exec(remoteHead)?.[1];
  if (
    !branch ||
    git("symbolic-ref", "--short", "HEAD") !== branch ||
    git("rev-parse", "HEAD") !== remoteCommit
  ) {
    throw new Error(
      "Release from the clean, pushed origin default branch. Pull/merge and push your changes first.",
    );
  }
  const tag = `v${chooseVersion(tags(), process.env.VERSION)}`;
  const commit = git("rev-parse", "HEAD");
  console.log(`Release ${tag} from ${branch} (${commit}) for server and desktop.`);
  if (process.env.DRY_RUN === "1") {
    console.log("Dry run: no tag created or pushed.");
    return;
  }
  git("tag", "-a", tag, commit, "-m", `ServerSend ${tag.slice(1)}`);
  try {
    git("push", "origin", `refs/tags/${tag}:refs/tags/${tag}`);
  } catch (error) {
    console.error(
      `Push failed; local ${tag} retained. Check the remote before retrying; do not move a published tag.`,
    );
    throw error;
  }
  console.log(
    "Release queued. Follow Actions → Release in GitHub for checks, desktop downloads and GHCR publication.",
  );
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    main();
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
