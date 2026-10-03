interface ForkChannelUpdater {
  allowPrerelease: boolean;
  allowDowngrade: boolean;
}

export function configureForkUpdateChannel(
  updater: ForkChannelUpdater,
  isForkBuild: boolean,
): void {
  if (!isForkBuild) return;

  // Fork tags vX.Y.Z.N are published releases; X.Y.Z-N is only the app's
  // semver encoding. Treating N as a prerelease channel makes GitHubProvider
  // reject every four-component tag before it can fetch latest*.yml.
  updater.allowPrerelease = false;
  // Architecture channel setters also enable downgrades; keep fork releases
  // monotonic regardless of which architecture channel was selected.
  updater.allowDowngrade = false;
}
