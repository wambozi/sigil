{ config, lib, pkgs, ... }:

let
  cfg = config.services.sigil-fleet;
in
{
  options.services.sigil-fleet = {
    enable = lib.mkEnableOption "Sigil Fleet Aggregation Layer";

    package = lib.mkOption {
      type = lib.types.package;
      default = pkgs.callPackage ./package.nix { };
      description = "The sigil-fleet package to use.";
    };

    listenAddr = lib.mkOption {
      type = lib.types.str;
      default = ":8090";
      description = "HTTP listen address.";
    };

    dbURL = lib.mkOption {
      type = lib.types.str;
      default = "postgres://sigil@localhost:5432/sigil_fleet?sslmode=disable";
      description = "PostgreSQL connection string.";
    };

    apiKeyFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      description = "Path to a file containing the API key for report ingestion.";
    };

    cloudCostPerQuery = lib.mkOption {
      type = lib.types.str;
      default = "0.01";
      description = "Cost per cloud AI query in dollars.";
    };

    oidc = {
      issuer = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = "OIDC issuer URL.";
      };
      clientID = lib.mkOption {
        type = lib.types.str;
        default = "";
        description = "OIDC client ID.";
      };
      clientSecretFile = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        description = "Path to a file containing the OIDC client secret.";
      };
      adminGroup = lib.mkOption {
        type = lib.types.str;
        default = "sigil-admins";
        description = "OIDC group claim for admin access.";
      };
    };

    postgresql = {
      enable = lib.mkEnableOption "local PostgreSQL for Sigil Fleet";
      database = lib.mkOption {
        type = lib.types.str;
        default = "sigil_fleet";
        description = "PostgreSQL database name.";
      };
    };
  };

  config = lib.mkIf cfg.enable {
    services.postgresql = lib.mkIf cfg.postgresql.enable {
      enable = true;
      ensureDatabases = [ cfg.postgresql.database ];
      ensureUsers = [
        {
          name = "sigil";
          ensureDBOwnership = true;
        }
      ];
    };

    systemd.services.sigil-fleet = {
      description = "Sigil Fleet Aggregation Layer";
      after = [ "network.target" ] ++ lib.optional cfg.postgresql.enable "postgresql.service";
      wants = lib.optional cfg.postgresql.enable "postgresql.service";
      wantedBy = [ "multi-user.target" ];

      environment = {
        FLEET_LISTEN_ADDR = cfg.listenAddr;
        FLEET_DB_URL = cfg.dbURL;
        FLEET_CLOUD_COST_PER_QUERY = cfg.cloudCostPerQuery;
        FLEET_OIDC_ISSUER = cfg.oidc.issuer;
        FLEET_OIDC_CLIENT_ID = cfg.oidc.clientID;
        FLEET_OIDC_ADMIN_GROUP = cfg.oidc.adminGroup;
      };

      serviceConfig = {
        ExecStart = "${cfg.package}/bin/fleet-service";
        Restart = "on-failure";
        RestartSec = 5;
        DynamicUser = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        NoNewPrivileges = true;
      } // lib.optionalAttrs (cfg.apiKeyFile != null) {
        EnvironmentFile = cfg.apiKeyFile;
      } // lib.optionalAttrs (cfg.oidc.clientSecretFile != null) {
        EnvironmentFile = cfg.oidc.clientSecretFile;
      };
    };
  };
}
