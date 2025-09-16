#!/usr/bin/env python3
"""
Elara Controller Results Plotter
================================

Script pour visualiser les résultats de simulation du contrôleur Elara.
Génère des graphiques en format PNG à partir des fichiers CSV.

Usage:
    python plot_results.py path/to/results.csv
"""

import argparse
import os
import sys
from pathlib import Path
from typing import Dict, List, Optional, Tuple

import pandas as pd
import plotly.graph_objects as go
from plotly.subplots import make_subplots
import plotly.colors as pcolors


class ElaraResultsPlotter:
    """Classe principale pour la visualisation des résultats Elara."""
    
    # Colonnes connues qui ne correspondent pas aux déploiements
    KNOWN_NON_DEPLOYMENT_COLS = {
        'Timestamp', 'TimeSeconds', 'PowerSignal', 
        'ReferencePower', 'ControllerMode', 'TotalReplicas'
    }
    
    # Mapping des couleurs pour les modes du contrôleur
    CONTROLLER_MODE_COLORS = {
        "Stable": "rgba(100, 100, 100, 0.1)",
        "PendingIncrease": "rgba(255, 165, 0, 0.15)",  # Orange
        "PendingDecrease": "rgba(0, 191, 255, 0.15)",  # Bleu
    }
    
    # Mapping des abréviations pour les modes du contrôleur
    CONTROLLER_MODE_ABBREVIATIONS = {
        "Stable": "S",
        "PendingIncrease": "I",
        "PendingDecrease": "D",
    }
    
    def __init__(self, csv_path: str):
        """Initialise le plotter avec le chemin vers le fichier CSV."""
        self.csv_path = Path(csv_path)
        self.df: Optional[pd.DataFrame] = None
        self.metadata: Dict[str, str] = {}
        self.deployment_cols: List[str] = []
        
    def load_data(self) -> None:
        """Charge les données depuis le fichier CSV."""
        try:
            self.df = pd.read_csv(self.csv_path)
            print(f"✓ Données chargées: {len(self.df)} lignes, {len(self.df.columns)} colonnes")
        except FileNotFoundError:
            print(f"❌ Erreur: Le fichier '{self.csv_path}' est introuvable.", file=sys.stderr)
            sys.exit(1)
        except Exception as e:
            print(f"❌ Erreur lors du chargement: {e}", file=sys.stderr)
            sys.exit(1)
    
    def load_metadata(self) -> None:
        """Charge les métadonnées depuis le fichier correspondant."""
        metadata_path = self._get_metadata_path()
        
        if not metadata_path.exists():
            print(f"ℹ️  Fichier de métadonnées non trouvé: '{metadata_path}'", file=sys.stderr)
            return
        
        try:
            meta_df = pd.read_csv(metadata_path)
            if 'Parameter' in meta_df.columns and 'Value' in meta_df.columns:
                self.metadata = dict(zip(meta_df['Parameter'], meta_df['Value']))
                print(f"✓ Métadonnées chargées: {len(self.metadata)} paramètres")
            else:
                print("⚠️  Format de métadonnées invalide (colonnes 'Parameter' et 'Value' attendues)", file=sys.stderr)
        except Exception as e:
            print(f"⚠️  Erreur lors du chargement des métadonnées: {e}", file=sys.stderr)
    
    def _get_metadata_path(self) -> Path:
        """Détermine le chemin du fichier de métadonnées."""
        base_name = self.csv_path.stem.replace('_results', '_metadata')
        return self.csv_path.parent / f"{base_name}.csv"
    
    def identify_deployment_columns(self) -> None:
        """Identifie automatiquement les colonnes de déploiement."""
        if self.df is None:
            raise ValueError("Les données doivent être chargées avant d'identifier les colonnes")
        
        self.deployment_cols = sorted([
            col for col in self.df.columns 
            if col not in self.KNOWN_NON_DEPLOYMENT_COLS
        ])
        print(f"✓ Colonnes de déploiement détectées: {self.deployment_cols}")
    
    def _format_metadata_html(self, detailed: bool = True) -> str:
        """Formate les métadonnées en HTML pour le titre."""
        if not self.metadata:
            return "<i>(Aucune métadonnée trouvée)</i>"
        
        if not detailed:
            # Version courte pour éviter les titres trop longs
            key_params = ['Deadband', 'IncreaseWindow', 'DecreaseWindow']
            short_params = {k: v for k, v in self.metadata.items() if k in key_params}
            if short_params:
                params_str = ", ".join([f"{k}={v}" for k, v in short_params.items()])
                return f"<i>Paramètres: {params_str}</i>"
            else:
                return f"<i>{len(self.metadata)} paramètres configurés</i>"
        
        # Version détaillée pour l'affichage complet
        params_html = "<b>Paramètres du Contrôleur:</b><br>"
        
        # Séparer les paramètres globaux des paramètres de déploiement
        global_params = {}
        deployment_params = {}
        
        for key, value in self.metadata.items():
            if '.' in key:
                deployment_params[key] = value
            else:
                global_params[key] = value
        
        # Afficher d'abord les paramètres globaux
        for key, value in global_params.items():
            params_html += f"&nbsp;&nbsp;{key}: {value}<br>"
        
        # Puis grouper les paramètres de déploiement
        if deployment_params:
            params_html += "&nbsp;&nbsp;<b>Services:</b><br>"
            current_service = None
            for key, value in sorted(deployment_params.items()):
                parts = key.split('.')
                if len(parts) >= 2:
                    service = f"{parts[0]}.{parts[1]}"
                    param = parts[-1]
                    
                    if service != current_service:
                        params_html += f"&nbsp;&nbsp;&nbsp;&nbsp;<u>{service}</u>:<br>"
                        current_service = service
                    
                    params_html += f"&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;{param}: {value}<br>"
        
        return params_html
    
    def _add_power_traces(self, fig: go.Figure) -> None:
        """Ajoute les traces de puissance au graphique."""
        # Signal de puissance
        fig.add_trace(
            go.Scatter(
                x=self.df['TimeSeconds'], 
                y=self.df['PowerSignal'],
                name='Signal de Puissance (W)',
                mode='lines',
                line=dict(color='royalblue', width=3),
                hovertemplate='<b>Puissance:</b> %{y:.2f}W<br><b>Temps:</b> %{x:.1f}s<extra></extra>'
            ),
            secondary_y=False,
        )
        
        # Puissance de référence
        fig.add_trace(
            go.Scatter(
                x=self.df['TimeSeconds'], 
                y=self.df['ReferencePower'],
                name='Puissance de Référence (P_ref)',
                mode='lines',
                line=dict(color='rgba(255, 87, 87, 0.7)', dash='dash'),
                hovertemplate='<b>P_ref:</b> %{y:.2f}W<br><b>Temps:</b> %{x:.1f}s<extra></extra>'
            ),
            secondary_y=False,
        )
    
    def _add_replica_traces(self, fig: go.Figure) -> None:
        """Ajoute les traces de replicas au graphique."""
        # Total des replicas
        fig.add_trace(
            go.Scatter(
                x=self.df['TimeSeconds'], 
                y=self.df['TotalReplicas'],
                name='Total Replicas',
                mode='lines',
                line=dict(color='black', width=4),
                hovertemplate='<b>Total:</b> %{y}<br><b>Temps:</b> %{x:.1f}s<extra></extra>'
            ),
            secondary_y=True,
        )
        
        # Replicas par déploiement
        color_sequence = pcolors.qualitative.Plotly
        for i, col_name in enumerate(self.deployment_cols):
            color = color_sequence[i % len(color_sequence)]
            fig.add_trace(
                go.Scatter(
                    x=self.df['TimeSeconds'], 
                    y=self.df[col_name],
                    name=col_name,
                    mode='lines',
                    line=dict(dash='dot', color=color),
                    hovertemplate=f'<b>{col_name}:</b> %{{y}}<br><b>Temps:</b> %{{x:.1f}}s<extra></extra>'
                ),
                secondary_y=True,
            )
    
    def _add_mode_annotations(self, fig: go.Figure) -> None:
        """Ajoute les annotations des changements de mode."""
        if 'ControllerMode' not in self.df.columns:
            return
        
        last_mode = self.df['ControllerMode'].iloc[0]
        
        for i in range(1, len(self.df)):
            current_mode = self.df['ControllerMode'].iloc[i]
            if current_mode != last_mode:
                fig.add_vrect(
                    x0=self.df['TimeSeconds'].iloc[i-1], 
                    x1=self.df['TimeSeconds'].iloc[i],
                    annotation_text=self.CONTROLLER_MODE_ABBREVIATIONS.get(last_mode, last_mode),
                    annotation_position="top left",
                    fillcolor=self.CONTROLLER_MODE_COLORS.get(
                        last_mode, "rgba(220, 220, 220, 0.1)"
                    ),
                    layer="below",
                    line_width=0,
                )
            last_mode = current_mode
    
    def create_plot(self, title: str, show_detailed_metadata: bool = True) -> go.Figure:
            """Crée le graphique principal avec une annotation pour les métadonnées."""
            if self.df is None:
                raise ValueError("Les données doivent être chargées avant de créer le graphique")
            
            fig = make_subplots(specs=[[{"secondary_y": True}]])
            
            self._add_power_traces(fig)
            self._add_replica_traces(fig)
            self._add_mode_annotations(fig)
            
            full_title = f"<b>Contrôleur Elara - {title}</b>"
            metadata_text = self._format_metadata_html(detailed=True)

            fig.update_layout(
                title_text=full_title,
                xaxis_title="Temps (secondes)",
                legend_title="<b>Métriques</b>",
                template="plotly_white",
                hovermode="x unified",
                # --- MODIFICATION ICI : Ajuster la marge du haut et de gauche ---
                margin=dict(t=120, l=280), # Augmenter un peu la marge du haut (t)
                width=1400,
                height=700,
                font=dict(size=12)
            )
            
            if show_detailed_metadata and self.metadata:
                fig.add_annotation(
                    text=metadata_text,
                    align='left',
                    showarrow=False,
                    xref='paper',
                    yref='paper',
                    x=-0.2,   # Positionne l'encadré à l'extérieur, sur la gauche
                    # --- MODIFICATION ICI : Descendre l'annotation (y) ---
                    y=0.9,    # Abaisser un peu l'annotation (était à 1.0)
                    bordercolor="black",
                    borderwidth=1,
                    bgcolor="rgba(240, 240, 240, 0.9)",
                    # Pour éviter le débordement, nous pouvons également limiter la largeur si nécessaire
                    # width=250 # Décommenter et ajuster si le texte déborde horizontalement
                )

            fig.update_yaxes(
                title_text="<b>Puissance (Watts)</b>", secondary_y=False,
                rangemode='tozero', showgrid=True, gridcolor='rgba(128, 128, 128, 0.2)'
            )
            fig.update_yaxes(
                title_text="<b>Nombre de Replicas</b>", secondary_y=True,
                rangemode='tozero', showgrid=False
            )
            
            return fig
    
    def _generate_safe_filename(self, base_name: str, extension: str = ".png") -> str:
        """Génère un nom de fichier sûr en évitant les noms trop longs."""
        # Nettoyer le nom de base
        safe_base = "".join(c for c in base_name if c.isalnum() or c in (' ', '-', '_')).strip()
        safe_base = safe_base.replace(' ', '_')
        
        # Limiter la longueur (système de fichiers Unix limite généralement à 255 caractères)
        max_length = 200  # Marge de sécurité
        if len(safe_base) > max_length:
            # Tronquer mais garder une partie identifiante
            safe_base = safe_base[:max_length-20] + "_" + str(hash(base_name) % 10000)
        
        return safe_base + extension
    
    def save_as_image(self, fig: go.Figure, output_path: str) -> None:
            """Sauvegarde le graphique en tant qu'image PNG."""
            try:
                # Sauvegarde en PNG avec haute résolution en utilisant le chemin fourni directement
                fig.write_image(output_path, width=1400, height=700, scale=2)
                print(f"✅ Image sauvegardée: {output_path}")
            except Exception as e:
                print(f"❌ Erreur lors de la sauvegarde: {e}", file=sys.stderr)
                print("💡 Assurez-vous d'avoir installé kaleido: pip install kaleido", file=sys.stderr)
                sys.exit(1)
    
    def save_metadata_summary(self, output_dir: str) -> None:
        """Sauvegarde un résumé des métadonnées dans un fichier texte séparé."""
        if not self.metadata:
            return
        
        output_path = Path(output_dir) / f"{self.csv_path.stem}_metadata_summary.txt"
        
        try:
            with open(output_path, 'w', encoding='utf-8') as f:
                f.write(f"Résumé des Métadonnées - {self.csv_path.name}\n")
                f.write("=" * 50 + "\n\n")
                
                # Séparer les paramètres globaux et de service
                global_params = {}
                deployment_params = {}
                
                for key, value in self.metadata.items():
                    if '.' in key:
                        deployment_params[key] = value
                    else:
                        global_params[key] = value
                
                # Écrire les paramètres globaux
                if global_params:
                    f.write("PARAMÈTRES GLOBAUX:\n")
                    f.write("-" * 20 + "\n")
                    for key, value in global_params.items():
                        f.write(f"  {key}: {value}\n")
                    f.write("\n")
                
                # Écrire les paramètres de service groupés
                if deployment_params:
                    f.write("PARAMÈTRES DES SERVICES:\n")
                    f.write("-" * 25 + "\n")
                    current_service = None
                    for key, value in sorted(deployment_params.items()):
                        parts = key.split('.')
                        if len(parts) >= 2:
                            service = f"{parts[0]}.{parts[1]}"
                            param = parts[-1]
                            
                            if service != current_service:
                                f.write(f"\n  {service}:\n")
                                current_service = service
                            
                            f.write(f"    {param}: {value}\n")
            
            print(f"📄 Résumé des métadonnées sauvegardé: {output_path}")
                
        except Exception as e:
            print(f"⚠️  Erreur lors de la sauvegarde du résumé: {e}", file=sys.stderr)
    
    def process_and_plot(self, title: Optional[str] = None, output_image: Optional[str] = None, 
                            detailed_metadata: bool = True, save_metadata: bool = False) -> None:
            """Pipeline complet: charge les données et crée le graphique."""
            print(f"🚀 Traitement du fichier: {self.csv_path}")
            
            # Chargement des données
            self.load_data()
            self.load_metadata()
            self.identify_deployment_columns()
            
            # Génération du titre si non fourni
            if title is None:
                title = self.csv_path.stem.replace('_results', '').replace('_', ' ').title()
            
            # Création du graphique
            fig = self.create_plot(title, show_detailed_metadata=detailed_metadata)
            
            # --- DÉBUT DE LA MODIFICATION ---

            # Détermination du chemin de sortie de l'image
            final_output_path: Path
            if output_image:
                # Si un chemin est fourni par l'utilisateur, on l'utilise
                final_output_path = Path(output_image)
            else:
                # Sinon, on le construit à partir du nom du fichier CSV
                # Le fichier PNG sera dans le même dossier que le CSV
                base_name = self.csv_path.stem
                final_output_path = self.csv_path.parent / f"{base_name}.png"
            
            # Sauvegarde en image
            print(f"🖼️  Génération de l'image PNG : {final_output_path}")
            self.save_as_image(fig, str(final_output_path))
            
            # Sauvegarde optionnelle du résumé des métadonnées
            if save_metadata:
                self.save_metadata_summary(str(final_output_path.parent))
                
            # --- FIN DE LA MODIFICATION ---
            
            print("✅ Traitement terminé!")


def main() -> None:
    """Point d'entrée principal du script."""
    parser = argparse.ArgumentParser(
        description="Visualise les résultats de simulation du contrôleur Elara",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Exemples d'utilisation:
  python plot_results.py test_output/scenario1_results.csv
  python plot_results.py data/experiment_results.csv -o mon_graphique.png
  python plot_results.py results.csv --title "Mon Scénario" --output ./images/scenario.png
  python plot_results.py results.csv --simple-metadata --save-metadata-summary
        """
    )
    
    parser.add_argument(
        "csv_file",
        type=str,
        help="Chemin vers le fichier CSV des résultats (ex: 'test_output/my_results.csv')"
    )
    
    parser.add_argument(
        "--title",
        type=str,
        help="Titre personnalisé pour le graphique"
    )
    
    parser.add_argument(
        "--output", "-o",
        type=str,
        help="Chemin de sortie pour l'image PNG (par défaut: nom_du_fichier.png)"
    )
    
    parser.add_argument(
        "--simple-metadata",
        action="store_true",
        help="Afficher seulement les métadonnées essentielles dans le titre (évite les titres trop longs)"
    )
    
    parser.add_argument(
        "--save-metadata-summary",
        action="store_true",
        help="Sauvegarder un fichier texte avec le résumé complet des métadonnées"
    )
    
    args = parser.parse_args()
    
    # Vérification de l'existence du fichier
    if not os.path.exists(args.csv_file):
        print(f"❌ Erreur: Le fichier '{args.csv_file}' n'existe pas.", file=sys.stderr)
        sys.exit(1)
    
    try:
        # Création et exécution du plotter
        plotter = ElaraResultsPlotter(args.csv_file)
        plotter.process_and_plot(
            title=args.title, 
            output_image=args.output,
            detailed_metadata=not args.simple_metadata,
            save_metadata=args.save_metadata_summary
        )
            
    except KeyboardInterrupt:
        print("\n❌ Opération annulée par l'utilisateur.")
        sys.exit(1)
    except Exception as e:
        print(f"❌ Erreur inattendue: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()