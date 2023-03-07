{
pkgs,
src,
version,
}:

let
  name = "ghcr.io/diagridio/dapr-cert-manager";

  binary = sys: os: (pkgs.buildGoApplication {
    name = "dapr-cert-manager";
    modules = ../gomod2nix.toml;
    inherit src;
    subPackages = [ "cmd" ];
  }).overrideAttrs(old: old // {
    GOOS = os;
    GOARCH = sys;
    CGO_ENABLED = "0";
    postInstall = ''
      mv $(find $out -type f) $out/bin/dapr-cert-manager
      find $out -empty -type d -delete
    '';
  });

  build = sys: tag: pkgs.dockerTools.buildLayeredImage {
    inherit name tag;
    contents = with pkgs; [
      (binary sys "linux")
    ];
  };

  publish = pkgs.writeShellApplication {
    name = "publish";
    runtimeInputs = with pkgs;[ podman ];
    text = ''
      if [[ -z "''${GITHUB_TOKEN}" ]]; then
        echo ">> Environment varibale 'GITHUB_TOKEN' is not set."
        exit 1
      fi

      echo ">> Logging into GitHub Container Registry..."
      echo "''${GITHUB_TOKEN}" | podman login ghcr.io -u $ --password-stdin

      echo ">> Pushing images..."
      podman manifest create ${name}:${version}
      podman manifest add ${name}:${version} docker-archive:${build "amd64" "${version}-amd64"} --os linux --arch amd64
      podman manifest add ${name}:${version} docker-archive:${build "arm64" "${version}-arm64"} --os linux --arch arm64
      podman push ${name}:${version}
    '';
  };

in {
  inherit binary build name;

  packages = {
    image-amd64 = (build "amd64" "${version}-amd64");
    image-arm64 = (build "arm64" "${version}-arm64");
  };

  apps = {
    image-publish = {type = "app"; program = "${publish}/bin/publish";};
  };
}
