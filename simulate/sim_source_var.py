import math
import time
import random
from dataclasses import dataclass, field
from typing import List, Optional, Dict, Any

# --- STRUCTURES DE DONNÉES (inchangées) ---
@dataclass
class Deployment:
    id: str
    replicas_actuels: int
    min_replicas: int
    max_replicas: int
    groupe_id: Optional[str] = None
    poids_ratio: Optional[float] = None

# --- SIMULATEUR DE SOURCE D'ÉNERGIE ---

class PowerSource:
    """
    Simule une source d'énergie dont la puissance fluctue autour d'une valeur optimale.
    """
    def __init__(self, optimal_power_kw: float, volatility: float = 0.15):
        """
        Args:
            optimal_power_kw: La puissance de référence du cluster en kW.
            volatility: Le facteur de volatilité (ex: 0.15 pour des fluctuations de +/- 15%).
        """
        self.optimal_power = optimal_power_kw
        self.volatility = volatility
        self.current_power = optimal_power_kw

    def get_current_power(self) -> float:
        """
        Génère une nouvelle valeur de puissance fluctuante.
        Pour une simulation plus réaliste, la nouvelle puissance est basée sur la précédente.
        """
        # Génère une fluctuation aléatoire
        change_factor = 1 + random.uniform(-self.volatility, self.volatility)
        
        # Simule une légère tendance à revenir vers l'optimal
        revert_to_mean = (self.optimal_power - self.current_power) * 0.1
        
        self.current_power = self.current_power * change_factor + revert_to_mean
        
        # S'assure que la puissance ne devient pas négative ou absurdement haute
        self.current_power = max(0, min(self.current_power, self.optimal_power * 1.5))
        
        return self.current_power

    def get_reduction_percentage(self) -> float:
        """
        Calcule le pourcentage de baisse par rapport à la puissance optimale.
        Retourne 0 si la puissance actuelle est supérieure ou égale à l'optimale.
        """
        current_power = self.get_current_power()
        if current_power >= self.optimal_power:
            return 0.0
        
        reduction = (self.optimal_power - current_power) / self.optimal_power
        return reduction


# --- CLASSE EnergyAwareScaler (inchangée, mais on la remet pour que le script soit complet) ---

class EnergyAwareScaler:
    def __init__(self, deployments: List[Deployment], reduction_percentage: float):
        if not (0 <= reduction_percentage <= 1):
            raise ValueError("Le pourcentage de réduction doit être entre 0 et 1.")
        self.deployments = deployments
        self.deployment_map = {d.id: d for d in deployments}
        self.reduction_percentage = reduction_percentage
        self.simulation_log = []

    def _log(self, message: str):
        print(message)
        self.simulation_log.append(message)

    def _distribute_integers_proportionally(self, total_to_distribute: int, entities: Dict[str, float]) -> Dict[str, int]:
        total_weight = sum(entities.values())
        if total_weight == 0: return {name: 0 for name in entities}
        brut_values = {name: (weight / total_weight) * total_to_distribute for name, weight in entities.items()}
        floor_values = {name: math.floor(val) for name, val in brut_values.items()}
        remainder_to_distribute = total_to_distribute - sum(floor_values.values())
        fractional_parts = {name: val - floor_values[name] for name, val in brut_values.items()}
        sorted_entities = sorted(fractional_parts.items(), key=lambda item: item[1], reverse=True)
        final_distribution = floor_values.copy()
        for i in range(int(remainder_to_distribute)):
            entity_name_to_increment = sorted_entities[i][0]
            final_distribution[entity_name_to_increment] += 1
        return final_distribution

    def run_simulation(self) -> Dict[str, Any]:
        self._log("--- DÉBUT DE LA SIMULATION DE SCALING ---")
        total_max_replicas = sum(d.max_replicas for d in self.deployments)
        global_reduction_target = math.ceil(total_max_replicas * self.reduction_percentage)
        self._log(f"Objectif global de réduction: {global_reduction_target} réplicas")
        if global_reduction_target == 0:
            self._log("Aucune réduction nécessaire. Fin de la simulation pour ce cycle.")
            return {'final_state': [d.__dict__ for d in self.deployments], 'unresolved_deficit': 0}

        entity_potentials = {}
        groups = {d.groupe_id for d in self.deployments if d.groupe_id}
        for group_id in groups:
            entity_potentials[group_id] = sum(d.max_replicas for d in self.deployments if d.groupe_id == group_id)
        for d in self.deployments:
            if d.groupe_id is None: entity_potentials[d.id] = d.max_replicas
        entity_reduction_targets = self._distribute_integers_proportionally(global_reduction_target, entity_potentials)
        
        desired_reductions = {}
        for entity_id, reduction_target in entity_reduction_targets.items():
            if entity_id in self.deployment_map and self.deployment_map[entity_id].groupe_id is None:
                desired_reductions[entity_id] = reduction_target
            else:
                deployments_in_group = [d for d in self.deployments if d.groupe_id == entity_id]
                group_weights = {d.id: d.poids_ratio for d in deployments_in_group}
                group_distribution = self._distribute_integers_proportionally(reduction_target, group_weights)
                desired_reductions.update(group_distribution)
        
        final_reductions = {}
        total_deficit = 0
        for d in self.deployments:
            max_possible_reduction = d.replicas_actuels - d.min_replicas
            desired = desired_reductions.get(d.id, 0)
            actual_reduction = min(desired, max_possible_reduction)
            final_reductions[d.id] = actual_reduction
            total_deficit += (desired - actual_reduction)
        
        iterations = 0
        while total_deficit > 0:
            iterations += 1
            if iterations > len(self.deployments) * 10: break
            donors = [{'deployment': d, 'margin': (d.replicas_actuels - d.min_replicas) - final_reductions.get(d.id, 0)} for d in self.deployments]
            donors = [d for d in donors if d['margin'] > 0]
            if not donors: break
            best_donor = max(donors, key=lambda x: x['margin'])
            final_reductions[best_donor['deployment'].id] += 1
            total_deficit -= 1
        
        final_state_data = []
        for d in self.deployments:
            removed = final_reductions.get(d.id, 0)
            new_replicas = d.replicas_actuels - removed
            final_state_data.append({
                'id': d.id, 'initial_replicas': d.replicas_actuels,
                'min_replicas': d.min_replicas, 'removed_replicas': removed,
                'final_replicas': new_replicas
            })
        
        return {'final_state': final_state_data, 'unresolved_deficit': total_deficit}

# --- BOUCLE DE SIMULATION PRINCIPALE ---

if __name__ == "__main__":
    SIMULATION_INTERVAL_SECONDS = 60  # Intervalle de temps en secondes
    
    # --- État initial du cluster ---
    cluster_state = [
        Deployment(id="webapp-server", groupe_id="frontend-group", replicas_actuels=10, min_replicas=2, max_replicas=20, poids_ratio=3.0),
        Deployment(id="asset-cdn", groupe_id="frontend-group", replicas_actuels=5, min_replicas=1, max_replicas=10, poids_ratio=1.0),
        Deployment(id="api-gateway", groupe_id="backend-group", replicas_actuels=8, min_replicas=4, max_replicas=15, poids_ratio=2.0),
        Deployment(id="user-service", groupe_id="backend-group", replicas_actuels=6, min_replicas=3, max_replicas=10, poids_ratio=2.0),
        Deployment(id="logging-service", replicas_actuels=3, min_replicas=2, max_replicas=5),
        Deployment(id="critical-db", replicas_actuels=2, min_replicas=2, max_replicas=3),
    ]
    
    # --- Configuration de la source d'énergie ---
    # Supposons que notre cluster consomme 100 kW en conditions optimales.
    power_source = PowerSource(optimal_power_kw=100.0, volatility=0.20)
    
    cycle = 0
    try:
        while True:
            cycle += 1
            print(f"\n\n{'='*20} CYCLE DE SIMULATION N°{cycle} {'='*20}")
            
            # 1. Obtenir le pourcentage de baisse de la source d'énergie
            reduction_percentage = power_source.get_reduction_percentage()
            current_power = power_source.optimal_power * (1 - reduction_percentage)
            
            print(f"Puissance Optimale: {power_source.optimal_power:.2f} kW")
            print(f"Puissance Actuelle: {current_power:.2f} kW")
            
            if reduction_percentage > 0:
                print(f"BAISSE DE PUISSANCE DÉTECTÉE: {reduction_percentage:.2%}")
                
                # 2. Lancer le scaler avec l'état actuel du cluster
                scaler = EnergyAwareScaler(cluster_state, reduction_percentage)
                result = scaler.run_simulation()
                
                # 3. Mettre à jour l'état du cluster pour le prochain cycle
                current_state_map = {d.id: d for d in cluster_state}
                for final_deployment_state in result['final_state']:
                    dep_id = final_deployment_state['id']
                    current_state_map[dep_id].replicas_actuels = final_deployment_state['final_replicas']

                # Affichage du nouvel état
                print("\n--- État du cluster après ajustement ---")
                for d in cluster_state:
                    print(f"  - {d.id:<25} : {d.replicas_actuels} réplicas")

            else:
                print("Puissance stable ou en hausse. Aucune action de réduction n'est nécessaire.")
                # NOTE : Ici, on pourrait implémenter la logique inverse de "scale-up" si besoin.
                # Pour l'instant, on ne fait rien.
            
            # 4. Attendre le prochain cycle
            print(f"\nProchain cycle dans {SIMULATION_INTERVAL_SECONDS} secondes...")
            time.sleep(SIMULATION_INTERVAL_SECONDS)
            
    except KeyboardInterrupt:
        print("\nSimulation terminée par l'utilisateur.")