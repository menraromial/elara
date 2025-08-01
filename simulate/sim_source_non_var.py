import math
from dataclasses import dataclass
from typing import List, Optional, Dict, Any

# --- ÉTAPE 0 : DÉFINITION DES STRUCTURES DE DONNÉES ---
# Représente un déploiement Kubernetes avec tous ses paramètres.

@dataclass
class Deployment:
    id: str
    replicas_actuels: int
    min_replicas: int
    max_replicas: int
    groupe_id: Optional[str] = None
    poids_ratio: Optional[float] = None

# --- CLASSE PRINCIPALE IMPLÉMENTANT LE MODÈLE ---

class EnergyAwareScaler:
    """
    Implémente le modèle mathématique de scaling dynamique basé sur un budget énergétique.
    """
    def __init__(self, deployments: List[Deployment], reduction_percentage: float):
        """
        Initialise le scaler avec l'état du cluster et l'objectif de réduction.

        Args:
            deployments: Une liste d'objets Deployment représentant l'état du cluster.
            reduction_percentage: Le pourcentage de réduction global à appliquer (ex: 0.20 pour 20%).
        """
        if not (0 <= reduction_percentage <= 1):
            raise ValueError("Le pourcentage de réduction doit être entre 0 et 1.")
            
        self.deployments = deployments
        self.deployment_map = {d.id: d for d in deployments}
        self.reduction_percentage = reduction_percentage
        self.simulation_log = []

    def _log(self, message: str):
        """Ajoute un message au log de la simulation."""
        print(message)
        self.simulation_log.append(message)

    def _distribute_integers_proportionally(self, total_to_distribute: int, entities: Dict[str, float]) -> Dict[str, int]:
        """
        Répartit un total entier proportionnellement en utilisant la méthode du plus grand reste.
        Ceci est crucial pour une répartition juste des arrondis.

        Args:
            total_to_distribute: Le nombre entier total à répartir (ex: Δ_total).
            entities: Un dictionnaire {id_entité: poids}.

        Returns:
            Un dictionnaire {id_entité: part_entière_attribuée}.
        """
        total_weight = sum(entities.values())
        if total_weight == 0:
            return {name: 0 for name in entities}

        # Calculer les parts brutes et les parts entières initiales
        brut_values = {name: (weight / total_weight) * total_to_distribute for name, weight in entities.items()}
        floor_values = {name: math.floor(val) for name, val in brut_values.items()}
        
        # Calculer le reste à distribuer
        remainder_to_distribute = total_to_distribute - sum(floor_values.values())
        
        # Calculer les parties fractionnaires pour le tri
        fractional_parts = {name: val - floor_values[name] for name, val in brut_values.items()}
        
        # Trier les entités par partie fractionnaire décroissante
        sorted_entities = sorted(fractional_parts.items(), key=lambda item: item[1], reverse=True)
        
        # Distribuer le reste
        final_distribution = floor_values.copy()
        for i in range(int(remainder_to_distribute)):
            entity_name_to_increment = sorted_entities[i][0]
            final_distribution[entity_name_to_increment] += 1
            
        return final_distribution

    def run_simulation(self) -> Dict[str, Any]:
        """Exécute l'algorithme de scaling complet, étape par étape."""
        self._log("--- DÉBUT DE LA SIMULATION DE SCALING ÉNERGÉTIQUE ---")

        # --- ÉTAPE 1 : Calcul de l'Objectif de Réduction Global ---
        self._log("\n--- ÉTAPE 1 : Calcul de l'Objectif de Réduction Global ---")
        total_max_replicas = sum(d.max_replicas for d in self.deployments)
        global_reduction_target = math.ceil(total_max_replicas * self.reduction_percentage)
        self._log(f"Potentiel max du cluster (Σ M_d): {total_max_replicas}")
        self._log(f"Pourcentage de réduction demandé (p): {self.reduction_percentage:.2%}")
        self._log(f"Objectif global de réduction (Δ_total = ⌈p * Σ M_d⌉): {global_reduction_target} réplicas")

        # --- ÉTAPE 2 : Répartition de l'Objectif entre Entités ---
        self._log("\n--- ÉTAPE 2 : Répartition de l'Objectif entre Entités (basée sur M_d) ---")
        entity_potentials = {}
        # Regrouper les déploiements par groupe et calculer le potentiel max de chaque entité
        groups = {d.groupe_id for d in self.deployments if d.groupe_id}
        for group_id in groups:
            entity_potentials[group_id] = sum(d.max_replicas for d in self.deployments if d.groupe_id == group_id)
        for d in self.deployments:
            if d.groupe_id is None:
                entity_potentials[d.id] = d.max_replicas

        self._log(f"Potentiels des entités (M_g, M_d): {entity_potentials}")
        entity_reduction_targets = self._distribute_integers_proportionally(global_reduction_target, entity_potentials)
        self._log(f"Cibles de réduction par entité (Δ_g, Δ_d): {entity_reduction_targets}")

        # --- ÉTAPE 3 : Calcul de la Réduction Voulue par Déploiement ---
        self._log("\n--- ÉTAPE 3 : Calcul de la Réduction Voulue par Déploiement (x_d^voulue) ---")
        desired_reductions = {}
        for entity_id, reduction_target in entity_reduction_targets.items():
            # Cas d'un déploiement indépendant
            if entity_id in self.deployment_map and self.deployment_map[entity_id].groupe_id is None:
                desired_reductions[entity_id] = reduction_target
            # Cas d'un groupe
            else:
                deployments_in_group = [d for d in self.deployments if d.groupe_id == entity_id]
                group_weights = {d.id: d.poids_ratio for d in deployments_in_group}
                self._log(f"  Répartition de {reduction_target} réplicas pour le groupe '{entity_id}' avec les poids {group_weights}")
                group_distribution = self._distribute_integers_proportionally(reduction_target, group_weights)
                desired_reductions.update(group_distribution)
        
        self._log(f"Réductions voulues par déploiement (x_d^voulue): {desired_reductions}")
        
        # --- ÉTAPE 4 : Ajustement Final sous Contraintes ---
        self._log("\n--- ÉTAPE 4 : Ajustement Final sous Contraintes (N_d) ---")
        
        # Calcul initial et déficit
        final_reductions = {}
        total_deficit = 0
        self._log("  Calcul des réductions possibles et du déficit initial...")
        for d in self.deployments:
            max_possible_reduction = d.replicas_actuels - d.min_replicas
            desired = desired_reductions.get(d.id, 0)
            
            actual_reduction = min(desired, max_possible_reduction)
            final_reductions[d.id] = actual_reduction
            
            deficit = desired - actual_reduction
            if deficit > 0:
                self._log(f"    - Déploiement '{d.id}': voulu={desired}, possible={max_possible_reduction} => réduction={actual_reduction}, déficit={deficit}")
            total_deficit += deficit
        
        self._log(f"  Déficit total à reporter: {total_deficit}")

        # Redistribution itérative du déficit
        if total_deficit > 0:
            self._log("  Redistribution itérative du déficit...")
        
        iterations = 0
        while total_deficit > 0:
            iterations += 1
            if iterations > len(self.deployments) * 10: # Failsafe
                self._log("  AVERTISSEMENT: Boucle de redistribution infinie détectée.")
                break

            donors = []
            for d in self.deployments:
                margin = (d.replicas_actuels - d.min_replicas) - final_reductions.get(d.id, 0)
                if margin > 0:
                    donors.append({'deployment': d, 'margin': margin})
            
            if not donors:
                self._log("  Aucun donneur disponible. La réduction maximale est atteinte.")
                break
                
            # Sélectionner le meilleur donneur (celui avec la plus grande marge)
            best_donor = max(donors, key=lambda x: x['margin'])
            donor_id = best_donor['deployment'].id
            
            self._log(f"    - Itération {iterations}: Le déficit ({total_deficit}) est reporté sur '{donor_id}' (marge: {best_donor['margin']})")
            
            final_reductions[donor_id] += 1
            total_deficit -= 1
        
        self._log(f"\nRéductions finales après ajustements (x_d^final): {final_reductions}")
        
        # --- SYNTHÈSE DU RÉSULTAT ---
        self._log("\n--- FIN DE LA SIMULATION : État final du cluster ---")
        final_state = []
        for d in self.deployments:
            removed = final_reductions.get(d.id, 0)
            new_replicas = d.replicas_actuels - removed
            final_state.append({
                'id': d.id,
                'groupe_id': d.groupe_id,
                'initial_replicas': d.replicas_actuels,
                'min_replicas': d.min_replicas,
                'max_replicas': d.max_replicas,
                'removed_replicas': removed,
                'final_replicas': new_replicas
            })
        
        # Affichage tabulaire
        print("-" * 110)
        print(f"{'Déploiement':<25} | {'Groupe':<15} | {'Initial':>7} | {'Min':>5} | {'Retirés':>7} | {'Final':>7} | {'Remarques'}")
        print("-" * 110)
        for state in final_state:
            remarques = []
            if state['final_replicas'] == state['min_replicas']:
                remarques.append("At Min")
            if state['removed_replicas'] < desired_reductions.get(state['id'], 0):
                remarques.append("Déficit initial")
            if state['removed_replicas'] > desired_reductions.get(state['id'], 0):
                remarques.append("A absorbé déficit")
            print(f"{state['id']:<25} | {state['groupe_id'] or 'Indépendant':<15} | {state['initial_replicas']:>7} | {state['min_replicas']:>5} | {state['removed_replicas']:>7} | {state['final_replicas']:>7} | {', '.join(remarques)}")
        print("-" * 110)
        
        if total_deficit > 0:
            self._log(f"\nAVERTISSEMENT: {total_deficit} réplicas n'ont pas pu être retirés à cause des contraintes 'min'.")
            
        return {'final_state': final_state, 'unresolved_deficit': total_deficit}


if __name__ == "__main__":
    # --- SCÉNARIO DE SIMULATION ---
    # Nous avons un cluster avec deux groupes et deux services indépendants.
    # Les contraintes sont variées pour tester tous les cas de figure.
    
    cluster_state = [
        # Groupe "frontend" - peu critique, grande marge de réduction
        Deployment(id="webapp-server", groupe_id="frontend-group", replicas_actuels=10, min_replicas=2, max_replicas=20, poids_ratio=3.0),
        Deployment(id="asset-cdn", groupe_id="frontend-group", replicas_actuels=5, min_replicas=1, max_replicas=10, poids_ratio=1.0),
        
        # Groupe "backend" - plus critique, moins de marge
        Deployment(id="api-gateway", groupe_id="backend-group", replicas_actuels=8, min_replicas=4, max_replicas=15, poids_ratio=2.0),
        Deployment(id="user-service", groupe_id="backend-group", replicas_actuels=6, min_replicas=3, max_replicas=10, poids_ratio=2.0),
        
        # Déploiement indépendant - déjà optimisé
        Deployment(id="logging-service", replicas_actuels=3, min_replicas=2, max_replicas=5),
        
        # Déploiement indépendant critique - aucune marge
        Deployment(id="critical-db", replicas_actuels=2, min_replicas=2, max_replicas=3),
    ]

    # Définir le pourcentage de réduction, par exemple une forte baisse de 25%
    REDUCTION_PERCENTAGE = 0.25

    # Lancer la simulation
    scaler = EnergyAwareScaler(cluster_state, REDUCTION_PERCENTAGE)
    scaler.run_simulation()