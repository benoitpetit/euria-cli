# Plan d'action — amélioration de `euria_retro`

> Document vivant. Priorités organisées par **niveau de risque** (R0 = critique/sécurité → R3 = dette technique). Chaque item indique l'impact, l'effort estimé, les fichiers concernés et les critères d'acceptation.

## Légende

- **Risque** : R0 critique · R1 fonctionnel/robustesse · R2 UX/produit · R3 dette technique
- **Effort** : S (< 1 j) · M (1–3 j) · L (> 3 j)
- **Statut** : ☐ à faire · ☑ fait · ⚠ bloqué

---

## Phase 0 — Stabilisation (R0/R1) : ne rien casser, réduire le bruit

### A0.1 — Réduire les avertissements non actionnables par défaut — R1 — Effort S ☑
- **Problème** : `auth status` sans sélection affiche ~10 warnings (keyring, profils absents, Safari macOS) pour un simple "not signed in".
- **Fichiers** : `internal/cli/run.go` (`warningsForReport`), `internal/session/sweetcookie.go`.
- **Action** : étendre le filtre par défaut pour ignorer aussi les messages contenant `keyring`, `secret-tool`, `supported on macOS only`, `v11 cookies`. Conserver l'affichage complet quand `--browser` est explicitement passé.
- **Acceptation** : `euria auth status` (sans flags) sur Linux sans session affiche au plus 1 ligne de warning pertinente ; avec `--browser chrome` affiche tout.
- **Tests** : `TestWarningsForReportFiltersNoisyDiagnostics` (table-driven).

### A0.2 — Parser les erreurs HTTP JSON — R1 — Effort S ☑
- **Problème** : `responseError` renvoie `Euria returned HTTP %d`, masquant le détail serveur.
- **Fichiers** : `internal/euria/client.go` (`responseError`).
- **Action** : lire jusqu'à N octets du corps, tenter de décoder `{"error":...}` / `{"message":...}` / `{"detail":...}`, sinon retomber sur `HTTP %d`. Ne jamais fuiter de cookies.
- **Acceptation** : sur HTTP 400 avec corps JSON, l'erreur contient le message serveur ; sur corps non-JSON, comportement actuel.
- **Tests** : `httptest` avec réponses JSON et texte brut.

### A0.3 — Exposer `--thinking` — R1 — Effort S ☑
- **Problème** : `ChatRequest.Thinking *bool` et le payload acceptent `thinking`, mais aucun flag.
- **Fichiers** : `internal/cli/options.go` (flag), `internal/cli/run.go` (`runChat`, `runGuestChat`), `internal/euria/client.go` (`GuestRequest.Thinking`).
- **Action** : ajouter `--thinking` (bool, défaut false) ; renseigner `Thinking` dans `ChatRequest` et `GuestRequest`. Ajouter `Thinking *bool` à `GuestRequest` et `guestPayload`.
- **Acceptation** : `--thinking --output json` envoie `thinking:true` (vérifiable via serveur de test) ; sans flag, pas de champ (omitempty).
- **Tests** : `TestRunChatSendsThinking`, `TestGuestChatSendsThinking`.

### A0.4 — Exposer `--project` — R1 — Effort S ☑
- **Problème** : `project_id` supporté à la création mais non pilotable.
- **Fichiers** : `internal/cli/options.go`, `internal/cli/run.go`.
- **Action** : flag `--project ID` ; passer `ProjectID` dans `CreateConversation` (signature à étendre) ; ignorer en mode continuation (le projet est celui de la conversation).
- **Acceptation** : création avec `--project` envoie `project_id`.
- **Tests** : test client + workflow.

### A0.5 — Outils étendus (`faq_search`, `wiki_search`, `create_pdf`) — R1 — Effort S ☑
- **Problème** : `toolsForSearch` ne connaît que `web_search`/`web_search_forced`.
- **Fichiers** : `internal/cli/options.go` (validation), `internal/cli/run.go` (`toolsForSearch`).
- **Action** : flag `--tool` répétable acceptant les 5 valeurs ; cumul avec `--web-search` (auto/on/off continue de piloter la recherche web). Valider l'ensemble.
- **Acceptation** : `--tool wiki_search --tool create_pdf` envoie les deux ; valeur inconnue → erreur de parsing.
- **Tests** : `TestParseTools`, `TestToolsForSearch`.

### A0.6 — `--timeout` pour l'attente de login — R2 — Effort S ☑
- **Fichiers** : `internal/cli/options.go`, `internal/cli/run.go`.
- **Action** : flag `--login-timeout` (durée, défaut 5m) alimentant `Dependencies.LoginTimeout`.
- **Acceptation** : valeur invalide rejetée ; valeur valide appliquée.

---

## Phase 1 — UX authentifiée (R2) : valeur produit immédiate

### A1.1 — Mode éphémère authentifié — R2 — Effort M ☑
- **Problème** : un utilisateur connecté ne peut pas chatter sans créer de conversation sauvegardée.
- **Fichiers** : `internal/euria/client.go` (nouvelle méthode `EphemeralConversation`), `internal/cli/run.go`.
- **Action** : endpoint `/api/1/accounts/{org}/conversations/ephemeral` avec le même payload que le public + cookies. Flag `--ephemeral` (authentifié uniquement).
- **Acceptation** : `--ephemeral --chat "..."` ne crée pas de conversation ; `conversation_id` peut être vide.
- **Tests** : client + workflow avec `httptest`.

### A1.2 — Streaming incrémental — R2 — Effort M ☑
- **Problème** : sortie bloquée jusqu'à la fin du stream.
- **Fichiers** : `internal/euria/stream.go`, `internal/cli/run.go`.
- **Action** : refactorer `ReadStream` pour accepter un callback `onDelta(string)` optionnel ; ajouter `--stream` qui affiche les deltas au fil de l'eau (texte uniquement ; JSON reste final).
- **Acceptation** : en mode texte, les premiers tokens apparaissent avant la fin ; en JSON, comportement actuel.
- **Tests** : stream simulé avec délais.

### A1.3 — Liste et recherche de conversations — R2 — Effort M ☑
- **Problème** : aucune commande de gestion.
- **Fichiers** : nouveau `internal/euria/conversations.go`, `internal/cli`.
- **Action** : `euria conversations list [--archived|--favorites]`, `euria conversations search QUERY`, `euria conversation show ID`.
- **Acceptation** : sortie texte et JSON ; pagination basique.
- **Tests** : client (décodage) + parsing.

### A1.4 — Renommer / archiver / favoriser — R2 — Effort M ☑
- **Action** : `euria conversation rename ID TITLE`, `archive`, `unarchive`, `favorite`, `unfavorite`.
- **Acceptation** : codes de retour corrects ; erreurs HTTP propagées.

### A1.5 — Partage de conversation — R2 — Effort M ☑
- **Action** : `euria conversation share ID` → affiche le lien de partage.
- **Acceptation** : retourne l'URL `…/shared/{shareId}`.

### A1.6 — Projets — R2 — Effort M
- **Action** : `euria projects list`, `euria project create NAME`, association `--project` déjà couverte en A0.4.
- **Acceptation** : CRUD minimal.

### A1.7 — Upload de fichiers — R2 — Effort L
- **Action** : `--file PATH` (répétable) → upload multipart puis `file_ids` dans le payload.
- **Acceptation** : fichier envoyé, ID récupéré, message créé avec pièce jointe.
- **Risque** : multipart, limites de taille, types MIME → tests robustes.

### A1.8 — Transcription audio — R3/R2 — Effort M ☑
### A1.9 — Mode vocal direct (`--voice`) — R2 — Effort M ☑
- Enregistrement via sox/arecord/ffmpeg, transcription via Euria, prompt automatique.
- **Action** : `euria audio transcript FILE`.
- **Acceptation** : retourne le texte.

---

## Phase 2 — Robustesse et dette technique (R3)

### A2.1 — Accès cookies via CDP (alternative à Sweetcookie) — R3 — Effort M ☑
- `--cdp URL` + `CDPCookieSource` testé end-to-end. La lecture SQLite directe reste possible mais le snapshot Sweetcookie est plus sûr (locks/WAL).
- **Problème** : dépendance Sweetcookie + warnings.
- **Action** : implémenter un lecteur read-only des DB `Cookies` (schéma Chromium) avec déchiffrement OS (Linux/macOS/Windows). Conserver Sweetcookie en fallback le temps de la migration.
- **Acceptation** : tests existants passent ; warnings réduits ; pas de copie temporaire.
- **Risque** : maintenance du schéma + chiffrement → isoler derrière une interface `CookieReader`.

### A2.2 — Complétion shell (bash/zsh/fish) — R3 — Effort M ☑
- **Action** : `euria completion bash|zsh|fish`, génération man.
- **Acceptation** : scripts valides.

### A2.3 — Couverture live authentifiée — R3 — Effort M
- **Action** : compte de test + smoke test manuel documenté ; ne pas mettre en CI public.

### A2.4 — Refactor `toolsForSearch` en table — R3 — Effort S
- **Action** : remplacer le switch par une map validée ; meilleure extensibilité.

### A2.5 — Normaliser les erreurs réseau — R3 — Effort S ☑
- **Action** : distinguer timeout vs. DNS vs. TLS dans le message (sans fuiter d'URL).

---

## Phase 3 — Gouvernance

### A3.1 — Changelog et versioning
- Mettre à jour `CHANGELOG.md` à chaque phase ; tag `v0.2.0` après Phase 0+1.

### A3.2 — Documentation
- Ajouter une section "Mode authentifié avancé" au README au fur et à mesure.

---

## Ordre d'exécution recommandé

1. Phase 0 complète (A0.1 → A0.6) — faible risque, base solide.
2. A1.1 et A1.2 (éphémère + streaming) — forte valeur UX.
3. A1.3 → A1.6 (gestion conversations/projets).
4. A1.7 (fichiers) et A1.8 (audio) — plus complexes.
5. Phase 2 selon besoin et temps.

## Critères de "done" globaux

- `make check && make test` verts sur Linux/macOS/Windows.
- Couverture `cli`/`euria` ≥ 80 %.
- Aucune régression du mode invité.
- Documentation README à jour.
- Changelog mis à jour.