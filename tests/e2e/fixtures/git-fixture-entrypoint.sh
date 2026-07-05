#!/bin/sh
set -e

mkdir -p /srv/git /work

for src in /repo-src/*/; do
  name=$(basename "$src")
  work="/work/${name}"
  bare="/srv/git/${name}.git"

  rm -rf "$work"
  cp -r "$src" "$work"

  git init --bare -q "$bare"

  git -C "$work" init -q
  git -C "$work" add -A
  git -c user.email=fixture@local -c user.name=fixture -C "$work" commit -q -m "fixture"
  git -C "$work" branch -M main
  git -C "$work" push -q "$bare" main

  git -C "$bare" symbolic-ref HEAD refs/heads/main
  git -C "$bare" update-server-info

  echo "Prepared fixture repo: ${name}.git"
done

exec httpd -f -p 80 -h /srv/git
