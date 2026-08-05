# Mode support — un salon, plusieurs conversations

## Le problème

Un salon Discord ne peut porter qu'une conversation. Le premier ping qui ne
demande pas de fil bind le salon à une session nommée `ch-<channelID>`, et tout
ping suivant tombe dessus. Ouvrir une deuxième conversation depuis ce salon
suppose que le ping contienne un mot déclencheur (`fil`, `thread`, `privé`), ce
qui n'est ni découvrable ni approprié dans un salon où plusieurs personnes
posent des questions indépendantes.

La conversation vit de toute façon dans le fil : c'est le fil qui porte la
session, et le routage se fait déjà par identifiant de conversation. Le salon
n'a pas besoin d'être bindé — il n'est qu'une rampe de lancement.

## Ce qu'on construit

Un **mode support** posé par salon. Dans un salon en mode support, chaque ping
ouvre un fil public accroché au message, avec sa propre session. Le salon n'est
jamais bindé, donc autant de conversations en parallèle que de pings, sans
ambiguïté d'adressage : chaque fil a son identifiant, et c'est déjà la clé de
routage.

Hors mode support, rien ne change.

## Où ça vit

Entièrement dans le plugin gateway (`router.go`, `routerstore.go`, `slash.go`,
`gateway.go`). Le core continue de recevoir un `contracts.CreateSession` dont le
`ChannelID` est une conversation neutre à adopter ; il n'apprend ni ce qu'est un
fil, ni ce qu'est un mode. `TestCoreNamesNoConcretePlatform` n'est pas concerné.

## Composants

### `bindStore` — deux mémoires de plus

```go
Modes map[string]string // channel id -> "support"
Repos map[string]string // channel id -> menu value ("local:x" / "remote:y")
```

Les deux sont `omitempty` et absents des stores écrits avant ce changement, ce
qui relit sans migration : un salon sans mode est un salon ordinaire, un salon
sans repo mémorisé pose la question.

Accesseurs calqués sur `Level`/`SetLevel` : `Mode`/`SetMode`, `Repo`/`SetRepo`,
une valeur vide supprimant l'entrée plutôt que d'en écrire une.

### `/mode` — poser le mode sur un salon

Commande slash calquée sur `/verbosity` : même `gate`, une option `mode` à deux
choix (`support`, `normal`), `SetMode` sur le salon où elle est tapée.

`/mode support` **débinde le salon** au passage. Un salon déjà bindé avant qu'on
y pose le mode continuerait sinon d'envoyer chaque ping vers son ancienne
session, et le mode n'aurait aucun effet visible. La session, elle, n'est pas
fermée : elle a peut-être du travail en cours, et `/session close` existe pour
ça.

### `job` — remplace deux maps parallèles

`conversation()` renvoie aujourd'hui `(conv string, thread bool)`, et le routeur
garde `threads map[string]bool` à côté de `pending`. Le repo mémorisé ajoute une
troisième valeur à trimballer — le salon d'où le job a été demandé — ce qui ferait
trois maps parallèles et un `bind()` à sept paramètres.

```go
// job is where one piece of work happens: the conversation it runs in, whether
// the gateway opened it, and the channel it was asked from.
type job struct {
	conv   string
	thread bool
	parent string
}
```

`conversation(ctx, m) job`, `bind(ctx, ctrl, j, value, m, buffered)`, et
`r.jobs map[string]job` à la place de `r.threads`.

### `client` — un fil public

```go
// StartThread opens a public thread hanging off a message. Everyone who reads
// the channel sees it, and its author is a member without being added.
StartThread(ctx context.Context, channelID, messageID, name string) (string, error)
```

Implémenté sur `dctl.Threads.Start`. C'est aussi le seul type de fil qui
n'exige pas « Créer des fils privés », la permission qui manque le plus souvent.

### `conversation()` — la branche support

Dans l'ordre, avant tout le reste :

1. Déjà dans un fil ouvert par le gateway → le job reste là (règle actuelle).
2. Salon en mode support → `StartThread(channel, m.ID, threadName(m.Content))`.
   Pas de `AddThreadMember` : l'auteur est membre par construction.
3. Le ping demande un fil (`wantsThread`) → fil privé, comme aujourd'hui.
4. Sinon → le salon lui-même.

Un échec d'ouverture en mode support suit la règle existante : c'est dit à voix
haute dans le salon, et le job y répond. Le salon se retrouve alors bindé — c'est
le prix d'un serveur mal configuré, et le message le dit.

### `ask()` — la question du repo, une fois par salon

Avant de poster le menu, si le salon parent a un repo mémorisé, binder dessus
directement. Après un bind réussi dans un salon support, mémoriser le repo
choisi sur le parent.

Le premier ticket pose la question une fois ; les suivants démarrent sans
question. Un `matchRepo` dans le ping alimente la même mémoire.

## Ce qui ne change pas

Le déclencheur (@mention dans un salon, message nu dans un fil ouvert par le
gateway), qui a le droit de piloter (`allowStore`), la verbosité (le fil hérite
du niveau de son salon), et la fermeture — un fil est un vrai thread Discord,
donc `Archive` le range au lieu de le supprimer.

## Tests

- Un salon sans mode se comporte exactement comme aujourd'hui.
- Deux pings successifs dans un salon support donnent deux sessions distinctes,
  et le salon n'est bindé ni après le premier ni après le second.
- Le repo mémorisé par le premier ticket évite le menu au second.
- Un fil qui ne peut pas s'ouvrir fait répondre dans le salon, et le dit.
- Un ping *dans* un fil support ne re-forke pas.
- `/mode support` débinde le salon ; `/mode normal` le remet dans le rang.
- Le store relit sans `Modes` ni `Repos` (format d'avant).
