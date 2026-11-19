# tucano-indices Helm chart

Chart Helm para publicar a API, executar o scheduler de ingestão e aplicar as migrations PostgreSQL do tucano-indices.

## Requisitos

- Kubernetes 1.25+ e Helm 3.
- PostgreSQL acessível pelo cluster. O chart não instala nem administra o banco.
- Imagem do GHCR disponível para o cluster. Releases finais publicam `ghcr.io/joselarantes/tucano-indices:<versão>`.
- Um Secret no namespace da instalação com as chaves `DATABASE_URL` e `API_TOKEN`.

Crie o Secret antes da instalação. Passe a URL e o token pelo seu mecanismo de secrets; não os grave em values versionados:

```sh
kubectl create namespace tucano-indices
kubectl -n tucano-indices create secret generic tucano-indices-secret \
  --from-literal=DATABASE_URL='postgres://USER:PASSWORD@HOST:5432/DATABASE?sslmode=require' \
  --from-literal=API_TOKEN='REPLACE_WITH_A_LONG_RANDOM_TOKEN'
```

Para imagens privadas no GHCR, crie um Secret Docker registry no namespace e configure `imagePullSecrets`.

## Instalação

```sh
helm upgrade --install tucano-indices ./deploy/tucano-indices \
  --namespace tucano-indices \
  --create-namespace \
  --set secrets.existingSecret=tucano-indices-secret \
  --set config.APP_PUBLIC_URL='https://indices.example.com'
```

O chart executa migrations como hook `pre-install,pre-upgrade`, inicia duas réplicas da API e uma do scheduler. O scheduler executa a sincronização nos horários configurados no serviço, no fuso `America/Sao_Paulo`. Para desligar um componente, ajuste `api.enabled` ou `scheduler.enabled`; migrations podem ser desativadas com `migrations.enabled=false`.

A API fica disponível pelo Service ClusterIP na porta 8080. Ingress é opcional e pode ser habilitado em `ingress`; configure também `config.APP_PUBLIC_URL` para o endereço externo real.

### BeakCloud e Project Contour

A integração específica do BeakCloud está ativa por padrão: ela cria um recurso `HTTPProxy` do Contour e um `Certificate` do cert-manager no namespace da release Helm. Instale/atualize o chart no namespace do serviço com:

```sh
helm upgrade --install tucano-indices ./deploy/tucano-indices \
  --namespace tucano-services \
  --reuse-values \
  --set beakcloud.enabled=true \
  --set config.APP_PUBLIC_URL='https://tucano-indices.beakcloud.com'
```

O bloco `beakcloud` em `values.yaml` controla o domínio, a classe `contour`, o `ClusterIssuer`, o Secret TLS e o destino da rota. Requeridos no cluster: CRDs do Project Contour e cert-manager, além do `ClusterIssuer` indicado. Para usar um certificado/Secret já existente, configure `beakcloud.certificate.create=false` e informe seu nome em `beakcloud.certificate.secretName`. Em clusters sem Contour/cert-manager, desative a integração com `--set beakcloud.enabled=false`.

Use a integração BeakCloud **ou** `ingress` para um mesmo domínio; não crie os dois recursos apontando para o mesmo FQDN.

## Versionamento da imagem

`Chart.yaml` começa na versão `1.0.0`. O release-please atualiza `version` e `appVersion` do chart junto com a versão do projeto. O workflow de publicação cria a tag sem prefixo `v` no GHCR, e o chart usa `appVersion` automaticamente para escolher a tag da imagem. Um override explícito permanece possível através de `image.tag`.

## Configuração

Consulte `values.yaml` para réplicas, requests/limits, ingress, HPA, URLs das fontes e demais valores. `DATABASE_URL` e `API_TOKEN` são fornecidos exclusivamente por Secret; o chart não armazena credenciais.

Validação local:

```sh
helm lint ./deploy/tucano-indices
helm template tucano-indices ./deploy/tucano-indices
```
