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

    version = "v0.1.0-rc2";

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

      image = import ./nix/image.nix {
        inherit pkgs src version;
      };

      ci = import ./nix/ci.nix {
        gomod2nix = (gomod2nix.packages.${system}.default);
        image = (image.build localSystem "dev");
        image-name = "${image.name}-${localSystem}";
        inherit src src-test repo pkgs;
      };

      localSystem = if pkgs.stdenv.hostPlatform.isAarch64 then "arm64" else "amd64";
      localOS = if pkgs.stdenv.hostPlatform.isDarwin then "darwin" else "linux";

    in {
      packages = {
        default = (image.binary localSystem localOS);
        image = (image.build localSystem "${version}-${localSystem}");
      } // image.packages;

      apps = {
        default = {type = "app"; program = "${self.packages.${system}.default}/bin/dapr-cert-manager"; };
      } // image.apps // ci.apps;

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
