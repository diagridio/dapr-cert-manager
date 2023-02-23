{ pkgs, gomod2nix, image, src, repo }:

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
    name = "dapr-cert-manager-helper-smoke";
    src = "${repo}/test/smoke";
    modules = "${repo}/test/smoke/gomod2nix.toml";
  }).overrideAttrs(old: old // {
    # We need to use a custom `buildPhase` so that we can build the smoke
    # binary using `go test` instead of `go build`.
    buildPhase = ''
      go test -v --race -o $GOPATH/bin/dapr-cert-manager-helper-smoke -c ./.
    '';
  });


  smoke = pkgs.writeShellApplication {
    name = "smoke";
    runtimeInputs = with pkgs; [
      kind
      kubernetes-helm
      kubectl
      dapr-cli
      docker
    ];
    text = ''
      tmpdir=$(mktemp -d)
      echo ">> using tmpdir: $tmpdir"
      trap 'rm -rf -- "$tmpdir"' EXIT

      trap 'kind delete cluster --name dapr-cert-manager-helper' EXIT
      kind create cluster --kubeconfig "$tmpdir/kubeconfig" --name dapr-cert-manager-helper --image kindest/node:v1.25.3

      docker load < ${image}
      kind load docker-image --name dapr-cert-manager-helper dapr-cert-manager-helper:dev
      export KUBECONFIG="$tmpdir/kubeconfig"
      echo ">> using kubeconfig: $KUBECONFIG"

      kubectl create namespace dapr-system

      helm repo add --force-update jetstack https://charts.jetstack.io
      helm upgrade -i cert-manager jetstack/cert-manager \
        --namespace cert-manager \
        --create-namespace \
        --set installCRDs=true \
        --wait &

      helm upgrade -i dapr-cert-manager-helper ${repo}/deploy/charts/dapr-cert-manager-helper \
        --namespace dapr-cert-manager-helper \
        --create-namespace \
        --set image.repository=dapr-cert-manager-helper \
        --set image.tag=dev \
        --set image.pullPolicy=Never \
        --set app.logLevel=4 \
        --wait &

      wait

      cat <<EOF | kubectl apply -f -
      apiVersion: cert-manager.io/v1
      kind: Issuer
      metadata:
        name: selfsigned
        namespace: dapr-system
      spec:
        selfSigned: {}
      ---
      apiVersion: cert-manager.io/v1
      kind: Certificate
      metadata:
        name: dapr-root-ca
        namespace: dapr-system
      spec:
        secretName: dapr-root-ca
        commonName: dapr-root-ca-from-cert-manager
        isCA: true
        issuerRef:
          name: selfsigned
      ---
      apiVersion: cert-manager.io/v1
      kind: Issuer
      metadata:
        name: dapr-trust-bundle
        namespace: dapr-system
      spec:
        ca:
          secretName: dapr-root-ca
      ---
      apiVersion: cert-manager.io/v1
      kind: Certificate
      metadata:
        name: dapr-trust-bundle
        namespace: dapr-system
      spec:
        secretName: dapr-trust-bundle-from-cert-manager
        commonName: dapr-issuer-from-cert-manager
        isCA: true
        dnsNames:
        - cluster.local
        issuerRef:
          name: dapr-trust-bundle
      EOF

      dapr init -k --wait

      ${smoke-binary}/bin/dapr-cert-manager-helper-smoke \
        --dapr-namespace dapr-system \
        --certificate-name dapr-trust-bundle \
        --kubeconfig "$KUBECONFIG"
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
  update = {type = "app"; program = "${update}/bin/update";};
  check = {type = "app"; program = "${check}/bin/check";};
  smoke = {type = "app"; program = "${smoke}/bin/smoke";};
}
