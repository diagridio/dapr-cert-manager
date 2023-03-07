{
pkgs,
gomod2nix,
image,
image-name,
src,
src-test,
repo,
}:

let
  checkgomod2nix = pkgs.writeShellApplication {
    name = "check-gomod2nix";
    runtimeInputs = [ gomod2nix ];
    text = ''
      tmpdir=$(mktemp -d)
      trap 'rm -rf -- "$tmpdir"' EXIT
      gomod2nix --dir "$1" --outdir "$tmpdir"
      if ! diff -q "$tmpdir/gomod2nix.toml" "$1/gomod2nix.toml"; then
        echo '>> gomod2nix.toml is not up to date. Please run:'
        echo '>> $ nix run .#update'
        exit 1
      fi
      echo '>> gomod2nix.toml is up to date'
    '';
  };

  smoke-binary = (pkgs.buildGoApplication {
    name = "dapr-cert-manager-smoke";
    src = "${src-test}/smoke";
    modules = "${src-test}/smoke/gomod2nix.toml";
  }).overrideAttrs(old: old // {
    # We need to use a custom `buildPhase` so that we can build the smoke
    # binary using `go test` instead of `go build`.
    buildPhase = ''
      go test -v --race -o $GOPATH/bin/dapr-cert-manager-smoke -c ./.
    '';
  });

  demo-loadimage = pkgs.writeShellApplication {
    name = "demo-loadimage";
    runtimeInputs = with pkgs; [
      podman
      systemd
    ];
    text = ''
      podman load < ${image}
      systemd-run --property=Delegate=yes --scope --user ${pkgs.kind}/bin/kind load docker-image --name dapr-cert-manager ${image-name}:dev
    '';
  };

  demo = pkgs.writeShellApplication {
    name = "demo";
    runtimeInputs = with pkgs; [
      demo-loadimage
      kubernetes-helm
      kubectl
      systemd
      dapr-cli
    ];
    text = ''
      TMPDIR="''${TMPDIR:-$(mktemp -d)}"
      echo ">> using tmpdir: $TMPDIR"

      systemd-run --property=Delegate=yes --scope --user ${pkgs.kind}/bin/kind create cluster --kubeconfig "$TMPDIR/kubeconfig" --name dapr-cert-manager

      ${demo-loadimage}/bin/demo-loadimage
      export KUBECONFIG="$TMPDIR/kubeconfig"
      echo ">> using kubeconfig: $KUBECONFIG"
      echo "export KUBECONFIG=$KUBECONFIG"
      echo ">> installing cert-manager, dapr-cert-manager and dapr"

      kubectl create namespace dapr-system

      dapr init -k --wait

      helm repo add --force-update jetstack https://charts.jetstack.io
      helm upgrade -i cert-manager jetstack/cert-manager \
        --namespace cert-manager \
        --create-namespace \
        --set installCRDs=true \
        --wait &

      helm upgrade -i dapr-cert-manager ${repo}/deploy/charts/dapr-cert-manager \
        --namespace dapr-cert-manager \
        --create-namespace \
        --set image.repository=${image-name} \
        --set image.tag=dev \
        --set image.pullPolicy=Never \
        --set app.logLevel=3 \
        --wait &

      wait

      echo ">> creating dapr root CA and intermediate CA"

      kubectl apply -f ${repo}/test/smoke/cert-manager-certs.yaml
    '';
  };

  smoke = pkgs.writeShellApplication {
    name = "smoke";
    runtimeInputs = with pkgs; [ systemd ];
    text = ''
      TMPDIR=$(mktemp -d)
      trap 'rm -rf -- "$TMPDIR"' EXIT
      trap 'systemd-run --property=Delegate=yes --scope --user ${pkgs.kind}/bin/kind delete cluster --name dapr-cert-manager' EXIT

      TMPDIR=$TMPDIR ${demo}/bin/demo

      echo ">> running smoke test"

      ${smoke-binary}/bin/dapr-cert-manager-smoke \
        --dapr-namespace dapr-system \
        --certificate-name-trust-bundle dapr-trust-bundle \
        --certificate-name-webhook dapr-webhook \
        --certificate-name-sidecar-injector dapr-sidecar-injector \
        --kubeconfig-path "$TMPDIR/kubeconfig"
    '';
  };

  update = pkgs.writeShellApplication {
    name = "update";
    runtimeInputs = [
      gomod2nix
    ];
    text = ''
      gomod2nix
      gomod2nix --dir test/smoke
      echo '>> Updated. Please commit the changes.'
    '';
  };

  check = pkgs.writeShellApplication {
    name = "check";
    runtimeInputs = [
      checkgomod2nix
    ];
    text = ''
      check-gomod2nix ${repo}
      check-gomod2nix ${repo}/test/smoke
    '';
  };

in {
  apps = {
    update = {type = "app"; program = "${update}/bin/update";};
    check = {type = "app"; program = "${check}/bin/check";};
    demo-loadimage = {type = "app"; program = "${demo-loadimage}/bin/demo-loadimage";};
    demo = {type = "app"; program = "${demo}/bin/demo";};
    smoke = {type = "app"; program = "${smoke}/bin/smoke";};
  };
}
