# Loom - Messagerie Unifiée

Loom est une application de bureau de messagerie unifiée construite avec Go, Wails, React et TypeScript. Elle vise à fournir une expérience de chat native, légère et multi-protocoles, en agrégeant des services comme WhatsApp, Slack et Google Messages.

## Architecture

Le projet suit les principes de la **Clean Architecture** pour une séparation claire des préoccupations et une meilleure maintenabilité.

-   **Backend (Go) :**
    -   `/pkg/core`: Contient la logique métier principale, y compris l'interface `Provider`.
    -   `/pkg/models`: Définit les structures de données (contacts, messages, etc.).
    -   `/pkg/db`: Gère l'initialisation de la base de données SQLite.
    -   `/pkg/providers`: Contient les adaptateurs pour chaque protocole de messagerie. Un `MockProvider` est inclus pour le développement.
-   **Frontend (React) :**
    -   `/frontend`: Contient l'application React, construite avec Vite et TypeScript.
    -   `/frontend/src/components`: Contient les composants React de l'interface utilisateur, construits avec **shadcn/ui**.
    -   `/frontend/src/lib`: Contient la logique partagée, y compris le store **Zustand**.
    -   `/frontend/src/locales`: Contient les fichiers de traduction pour **react-i18next**.

## Stack Technique

-   **Backend :** Go 1.21+
-   **Desktop Runtime :** Wails v2
-   **Base de Données :** SQLite (via `glebarez/sqlite`)
-   **Frontend :** React + TypeScript + Vite
-   **UI :** TailwindCSS + **shadcn/ui**
-   **Gestion d'état :** Zustand
-   **Internationalisation :** react-i18next

---

## Instructions de Développement et de Compilation

### Prérequis

Avant de commencer, assurez-vous d'avoir les outils suivants installés :

1.  **Go (1.21 ou plus récent) :** [https://golang.org/dl/](https://golang.org/dl/)
2.  **Node.js (LTS) :** [https://nodejs.org/](https://nodejs.org/)
3.  **Wails CLI v2 :**
    ```bash
    go install github.com/wailsapp/wails/v2/cmd/wails@latest
    ```

### Lancer en Mode Développement

Le mode développement est idéal pour travailler sur l'application, car il offre le rechargement à chaud (hot-reloading) pour le frontend et le backend.

1.  **Installer les dépendances du Frontend :**
    Naviguez vers le dossier `frontend` et installez les paquets npm.
    ```bash
    cd frontend
    npm install
    ```

2.  **Lancer l'application :**
    Revenez au dossier racine du projet et exécutez `wails dev`.
    ```bash
    cd ..
    wails dev
    ```
    Cela compilera le backend, démarrera le serveur de développement Vite et lancera l'application de bureau.

### Compiler pour la Production

Pour créer un binaire d'application final :

1.  **Installer les dépendances du Frontend (si ce n'est pas déjà fait) :**
    ```bash
    cd frontend
    npm install
    ```

2.  **Construire le Frontend :**
    Cette étape génère le dossier `dist` qui sera embarqué dans l'application Go.
    ```bash
    npm run build
    ```

3.  **Compiler l'application Go :**
    Revenez au dossier racine et lancez la commande de build de Wails.
    ```bash
    cd ..
    wails build
    ```
    L'exécutable final se trouvera dans le dossier `build/bin`.
