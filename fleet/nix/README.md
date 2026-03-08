# Aether Fleet NixOS Module

Deploy the Fleet Aggregation Layer on NixOS.

## Usage

Add to your NixOS configuration:

```nix
{ pkgs, ... }:
{
  imports = [ ./path/to/fleet-aggregation.nix ];

  services.aether-fleet = {
    enable = true;
    listenAddr = ":8090";
    dbURL = "postgres://aether@localhost:5432/aether_fleet?sslmode=disable";
    apiKeyFile = "/run/secrets/fleet-api-key";
    cloudCostPerQuery = "0.01";

    postgresql.enable = true;

    oidc = {
      issuer = "https://accounts.google.com";
      clientID = "your-client-id";
      clientSecretFile = "/run/secrets/oidc-client-secret";
      adminGroup = "aether-admins";
    };
  };
}
```

## Options

| Option | Type | Default | Description |
|---|---|---|---|
| `enable` | bool | `false` | Enable the Fleet Aggregation Layer |
| `listenAddr` | string | `":8090"` | HTTP listen address |
| `dbURL` | string | local postgres | PostgreSQL connection string |
| `apiKeyFile` | path | `null` | File containing FLEET_API_KEY |
| `cloudCostPerQuery` | string | `"0.01"` | Cost per cloud query ($) |
| `postgresql.enable` | bool | `false` | Deploy local PostgreSQL |
| `oidc.issuer` | string | `""` | OIDC issuer URL |
| `oidc.clientID` | string | `""` | OIDC client ID |
| `oidc.clientSecretFile` | path | `null` | File containing OIDC secret |
| `oidc.adminGroup` | string | `"aether-admins"` | Admin group claim |
