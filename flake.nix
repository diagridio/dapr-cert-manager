{
  description = "dapr-cert-manager";

  inputs = {
    nixpkgs.url = "nixpkgs/nixos-unstable";
    utils.url = "github:numtide/flake-utils";

    gomod2nix = {
      url = "github:tweag/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.utils.follows = "utils";
    };
  };

  outputs = { self, nixpkgs, utils, gomod2nix }:
  let
    targetSystems = with utils.lib.system; [
      x86_64-linux
      x86_64-darwin
      aarch64-linux
      aarch64-darwin
    ];

    repo = ./.;

    # We only source go files to have better cache hits when actively working
    # on non-go files.
    src = nixpkgs.lib.sourceFilesBySuffices ./. [ ".go" "go.mod" "go.sum" "gomod2nix.toml" ];
    src-test = nixpkgs.lib.sourceFilesBySuffices ./test [ ".go" "go.mod" "go.sum" "gomod2nix.toml" ];

    version = "0.1.0";

  in utils.lib.eachSystem targetSystems (system:
    let
      pkgs = import nixpkgs {
        inherit system;
        overlays = [
          (final: prev: {
            go = prev.go_1_20;
            buildGoApplication = prev.buildGo120Application;
          })
          gomod2nix.overlays.default
        ];
      };

      program = sys: os: (pkgs.buildGoApplication {
        name = "dapr-cert-manager";
        modules = ./gomod2nix.toml;
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

      image = sys: tag: pkgs.dockerTools.buildLayeredImage {
        name = "dapr-cert-manager";
        inherit tag;
        contents = with pkgs; [
          (program sys "linux")
        ];
      };

      ci = import ./nix/ci.nix {
        gomod2nix = (gomod2nix.packages.${system}.default);
        image = (image localSystem "dev");
        inherit src src-test repo pkgs;
      };

      localSystem = if pkgs.stdenv.hostPlatform.isAarch64 then "arm64" else "amd64";
      localOS = if pkgs.stdenv.hostPlatform.isDarwin then "darwin" else "linux";

    in {
      packages = {
        default = (program localSystem localOS);
        image = (image localSystem version);
        image-x86_64 = (image "amd64" version);
        image-arm64 = (image "arm64" version);
      };

      apps = {
        inherit (ci) check update smoke demo demo-loadimage;
        default = {type = "app"; program = "${self.packages.${system}.default}/bin/dapr-cert-manager"; };
      };

      devShells.default = pkgs.mkShell {
        buildInputs = with pkgs; [
          go
          gopls
          gotools
          go-tools
          gomod2nix.packages.${system}.default
        ];
      };
  });
}
