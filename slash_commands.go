package discord

import (
	"github.com/Herrscherd/dctl"
)

func commandSet() *dctl.Command {
	return dctl.NewCommand("set", "configurer le démon").
		Perms(dctl.PermManageGuild).
		With(
			dctl.Sub("home", "définir la catégorie ou le forum qui accueille les salons de session",
				dctl.ChannelOpt("channel", "salon de catégorie ou de forum", true)),
			dctl.Sub("source", "définir le dépôt source à partir duquel `service update` compile",
				dctl.String("path", "chemin absolu du dépôt source", true)),
		)
}

func commandSession() *dctl.Command {
	return dctl.NewCommand("session", "gérer les sessions").
		Perms(dctl.PermManageGuild).
		With(
			dctl.Sub("create", "créer une session : un salon relié, un worktree isolé et un backend",
				dctl.String("name", "nom de la session", true),
				dctl.String("cmd", "commande reliée (par défaut la commande configurée)", false),
				dctl.Bool("shared", "lancer dans le dépôt principal au lieu d'un worktree isolé", false),
				dctl.String("backend", "backend du pont", false).
					Choices(dctl.NewChoice("stream", "stream"), dctl.NewChoice("oneshot", "oneshot")),
				dctl.String("project", "sous-dossier de l'espace de travail où travaille le backend", false),
				dctl.String("clone", "dépôt distant (propriétaire/nom) à cloner d'abord dans l'espace de travail", false),
			),
			dctl.Sub("close", "fermer une session : arrêter le pont, supprimer le worktree, archiver le salon",
				dctl.String("name", "nom de la session", true).Autocomplete(),
				dctl.Bool("force", "abandonner les modifications non commitées du worktree", false),
			),
			dctl.Sub("list", "lister les sessions actives"),
			dctl.Sub("who", "lister les participants observés dans une session",
				dctl.String("name", "nom de la session", true).Autocomplete()),
			dctl.Group("allow", "gérer qui peut participer à une session",
				dctl.Sub("add", "autoriser un utilisateur dans une session",
					dctl.String("name", "nom de la session", true).Autocomplete(),
					dctl.User("user", "utilisateur à autoriser", true)),
				dctl.Sub("remove", "retirer l'autorisation d'un utilisateur dans une session",
					dctl.String("name", "nom de la session", true).Autocomplete(),
					dctl.User("user", "utilisateur à retirer", true)),
				dctl.Sub("list", "lister les utilisateurs autorisés dans une session",
					dctl.String("name", "nom de la session", true).Autocomplete()),
			),
		)
}

func commandService() *dctl.Command {
	return dctl.NewCommand("service", "gérer le démon").
		Perms(dctl.PermManageGuild).
		With(
			dctl.Sub("restart", "redémarrer le démon"),
			dctl.Sub("update", "recompiler le démon depuis les sources et le redémarrer",
				dctl.Bool("no_pull", "ne pas faire de git pull avant la compilation", false)),
		)
}

func commandStop() *dctl.Command {
	return dctl.NewCommand("stop", "annuler le tour en cours dans cette conversation").
		Perms(dctl.PermManageGuild)
}

func commandVerbosity() *dctl.Command {
	return dctl.NewCommand("verbosity", "régler ce que cette conversation affiche d'un tour").
		Perms(dctl.PermManageGuild).
		With(
			dctl.String("level", "ce qu'il faut afficher d'un tour ici", true).
				Choices(
					dctl.NewChoice("silencieux : seulement la réponse", levelSilent),
					dctl.NewChoice("discret : aussi la liste en direct des noms d'outils", levelQuiet),
					dctl.NewChoice("actions : aussi le détail de chaque outil", levelActions),
					dctl.NewChoice("complet : aussi le texte de l'assistant", levelFull),
				),
		)
}

func commandMode() *dctl.Command {
	return dctl.NewCommand("mode", "régler comment ce salon traite les mentions").
		Perms(dctl.PermManageGuild).
		With(
			dctl.String("mode", "comment les mentions sont traitées ici", true).
				Choices(
					dctl.NewChoice("normal : le bot répond quand il est mentionné", normalMode),
					dctl.NewChoice("support : chaque mention ouvre son propre fil", supportMode),
					dctl.NewChoice("ambiant : le bot répond ici sans être mentionné", ambientMode),
					dctl.NewChoice("désactivé : le bot ignore entièrement ce salon", offMode),
				),
		)
}

func commandAllow() *dctl.Command {
	return dctl.NewCommand("allow", "gérer qui peut utiliser le bot").
		Perms(dctl.PermManageGuild).
		With(
			dctl.Sub("add", "autoriser un utilisateur à utiliser le bot", dctl.User("user", "utilisateur à autoriser", true)),
			dctl.Sub("remove", "retirer un utilisateur de la liste d'autorisation", dctl.User("user", "utilisateur à retirer", true)),
			dctl.Sub("list", "lister les utilisateurs autorisés à utiliser le bot"),
		)
}
