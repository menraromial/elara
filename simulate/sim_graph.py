import math
import time
import random
import logging
from dataclasses import dataclass
from typing import List, Optional, Dict, Any

# --- CONFIGURATION DES LOGS ---
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s',
    datefmt='%Y-%m-%d %H:%M:%S'
)

# --- STRUCTURES DE DONNÉES ---
@dataclass
class Deployment:
    id: str
    replicas_actuels: int
    min_replicas: int
    max_replicas: int
    groupe_id: Optional[str] = None
    poids_ratio: Optional[float] = None

# --- GESTIONNAIRE DE GRAPHIQUE ---
try:
    import matplotlib.pyplot as plt
    MATPLOTLIB_AVAILABLE = True
except ImportError:
    logging.warning("Matplotlib non trouvé. Les graphiques ne seront pas affichés. Installez-le avec : pip install matplotlib")
    MATPLOTLIB_AVAILABLE = False

class RealTimePlotter:
    def __init__(self, deployment_ids: List[str]):
        if not MATPLOTLIB_AVAILABLE: return
        self.fig, (self.ax1, self.ax2) = plt.subplots(2, 1, figsize=(12, 9), sharex=True)
        plt.ion()
        self.timestamps, self.optimal_power_data, self.current_power_data, self.total_replicas_data = [], [], [], []
        self.individual_replicas_data = {dep_id: [] for dep_id in deployment_ids}
        self.ax1.set_title("Évolution de la Puissance Électrique et du Total des Réplicas")
        self.ax1.set_ylabel("Puissance (kW)", color='tab:blue')
        self.line_optimal_power, = self.ax1.plot([], [], 'k--', label="Puissance Optimale")
        self.line_current_power, = self.ax1.plot([], [], 'b-', label="Puissance Actuelle", lw=2)
        self.ax1b = self.ax1.twinx()
        self.ax1b.set_ylabel("Total Réplicas", color='tab:red')
        self.line_total_replicas, = self.ax1b.plot([], [], 'r-', label="Total Réplicas")
        self.fig.legend(loc="upper center", bbox_to_anchor=(0.5, 1.0), ncol=3)
        self.ax1.grid(True)
        self.ax2.set_title("Évolution des Réplicas par Déploiement")
        self.ax2.set_xlabel("Cycle de Simulation")
        self.ax2.set_ylabel("Nombre de Réplicas")
        self.individual_lines = {dep_id: self.ax2.plot([], [], label=dep_id)[0] for dep_id in deployment_ids}
        self.ax2.legend(loc="upper left", bbox_to_anchor=(1.05, 1.0))
        self.fig.tight_layout(rect=[0, 0, 0.85, 0.95])

    def update(self, cycle, optimal_power, current_power, total_replicas, individual_replicas):
        if not MATPLOTLIB_AVAILABLE: return
        self.timestamps.append(cycle)
        self.optimal_power_data.append(optimal_power)
        self.current_power_data.append(current_power)
        self.total_replicas_data.append(total_replicas)
        self.line_optimal_power.set_data(self.timestamps, self.optimal_power_data)
        self.line_current_power.set_data(self.timestamps, self.current_power_data)
        self.line_total_replicas.set_data(self.timestamps, self.total_replicas_data)
        for dep_id, replicas in individual_replicas.items():
            self.individual_replicas_data[dep_id].append(replicas)
            self.individual_lines[dep_id].set_data(self.timestamps, self.individual_replicas_data[dep_id])
        for ax in [self.ax1, self.ax1b, self.ax2]:
            ax.relim(), ax.autoscale_view()
        self.fig.canvas.draw(), self.fig.canvas.flush_events()
        plt.pause(0.01)

# --- SOURCE D'ÉNERGIE CORRIGÉE ---
class PowerSource:
    def __init__(self, optimal_power_kw: float, volatility: float = 0.15):
        self.optimal_power = optimal_power_kw
        self.volatility = volatility
        self.current_power = optimal_power_kw

    def update_and_get_power(self) -> float:
        """Met à jour la puissance et la retourne. C'est LA méthode à appeler à chaque cycle."""
        change_factor = 1 + random.uniform(-self.volatility, self.volatility)
        revert_to_mean = (self.optimal_power - self.current_power) * 0.1
        self.current_power = self.current_power * change_factor + revert_to_mean
        self.current_power = max(0, min(self.current_power, self.optimal_power * 1.5))
        return self.current_power

    def get_reduction_percentage(self) -> float:
        """Calcule le pourcentage de baisse basé sur la puissance ACTUELLE."""
        if self.current_power >= self.optimal_power:
            return 0.0
        return (self.optimal_power - self.current_power) / self.optimal_power

# --- SCALER AMÉLIORÉ ---
class EnergyAwareScaler:
    # ... le reste de la classe est complexe et reste majoritairement le même
    def __init__(self, deployments: List[Deployment]):
        self.deployments = deployments
        self.deployment_map = {d.id: d for d in deployments}

    def _distribute_integers_proportionally(self, total_to_distribute, entities):
        # ... Logique inchangée ...
        total_weight = sum(entities.values())
        if total_weight == 0: return {name: 0 for name in entities}
        brut_values = {name: (weight / total_weight) * total_to_distribute for name, weight in entities.items()}
        floor_values = {name: math.floor(val) for name, val in brut_values.items()}
        remainder_to_distribute = total_to_distribute - sum(floor_values.values())
        fractional_parts = {name: val - floor_values[name] for name, val in brut_values.items()}
        sorted_entities = sorted(fractional_parts.items(), key=lambda item: item[1], reverse=True)
        final_distribution = floor_values.copy()
        for i in range(int(remainder_to_distribute)):
            final_distribution[sorted_entities[i][0]] += 1
        return final_distribution
        
    def run_scale_down(self, reduction_percentage: float) -> Dict[str, Any]:
        # Identique à l'ancienne méthode run_simulation
        logging.info("--- DÉBUT DU CALCUL DE SCALE-DOWN ---")
        total_max_replicas = sum(d.max_replicas for d in self.deployments)
        global_reduction_target = math.ceil(total_max_replicas * reduction_percentage)
        # ... Le reste de la logique de scale-down est identique à la version précédente
        # (calcul par entité, par déploiement, gestion du déficit, etc.)
        logging.info(f"Objectif global de réduction: {global_reduction_target} réplicas")
        if global_reduction_target == 0:
            return {'final_state': [], 'unresolved_deficit': 0}
        
        # Le reste de la logique de scale-down est ici...
        entity_potentials = {}
        groups = {d.groupe_id for d in self.deployments if d.groupe_id}
        for group_id in groups: entity_potentials[group_id] = sum(d.max_replicas for d in self.deployments if d.groupe_id == group_id)
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

        final_reductions, total_deficit = {}, 0
        for d in self.deployments:
            max_possible = d.replicas_actuels - d.min_replicas
            desired = desired_reductions.get(d.id, 0)
            actual = min(desired, max_possible)
            final_reductions[d.id] = actual
            total_deficit += (desired - actual)
        
        while total_deficit > 0:
            donors = [{'d': d, 'm': (d.replicas_actuels - d.min_replicas) - final_reductions.get(d.id, 0)} for d in self.deployments]
            donors = [d for d in donors if d['m'] > 0]
            if not donors: break
            best_donor = max(donors, key=lambda x: x['m'])
            final_reductions[best_donor['d'].id] += 1
            total_deficit -= 1
        
        logging.info(f"Réductions finales: {final_reductions}")
        return {
            'final_state': [{
                'id': d.id, 
                'final_replicas': d.replicas_actuels - final_reductions.get(d.id, 0)
            } for d in self.deployments],
            'unresolved_deficit': total_deficit
        }

    def run_scale_up(self, scale_up_factor: float = 0.25) -> Dict[str, Any]:
        """Ajoute des réplicas pour revenir vers l'état max, proportionnellement."""
        logging.info("--- DÉBUT DU CALCUL DE SCALE-UP ---")
        
        # Calculer le "manque" total de réplicas pour atteindre le max
        total_missing_replicas = sum(d.max_replicas - d.replicas_actuels for d in self.deployments)
        if total_missing_replicas == 0:
            logging.info("Tous les déploiements sont déjà à leur maximum. Aucune action.")
            return {'final_state': []}
            
        # On ne remonte qu'une fraction du manque pour éviter une montée en charge trop brutale
        replicas_to_add_total = math.floor(total_missing_replicas * scale_up_factor)
        if replicas_to_add_total == 0:
            logging.info("Le retour à la normale est trop faible pour ajouter un réplica ce cycle.")
            return {'final_state': []}

        logging.info(f"Manque total de {total_missing_replicas} réplicas. Tentative d'ajout de {replicas_to_add_total} réplicas.")

        # Répartir les réplicas à ajouter proportionnellement au potentiel max
        potentials = {d.id: d.max_replicas for d in self.deployments}
        desired_additions = self._distribute_integers_proportionally(replicas_to_add_total, potentials)

        # Appliquer les ajouts en respectant le max de chacun
        final_additions = {}
        for d in self.deployments:
            can_add = d.max_replicas - d.replicas_actuels
            wants_to_add = desired_additions.get(d.id, 0)
            final_additions[d.id] = min(can_add, wants_to_add)

        logging.info(f"Ajouts finaux de réplicas: {final_additions}")
        return {
            'final_state': [{
                'id': d.id, 
                'final_replicas': d.replicas_actuels + final_additions.get(d.id, 0)
            } for d in self.deployments]
        }


# --- BOUCLE DE SIMULATION PRINCIPALE CORRIGÉE ---
if __name__ == "__main__":
    SIMULATION_INTERVAL_SECONDS = 5
    
    cluster_state = [Deployment(id="webapp-server", groupe_id="frontend-group", replicas_actuels=10, min_replicas=2, max_replicas=20, poids_ratio=3.0), Deployment(id="asset-cdn", groupe_id="frontend-group", replicas_actuels=5, min_replicas=1, max_replicas=10, poids_ratio=1.0), Deployment(id="api-gateway", groupe_id="backend-group", replicas_actuels=8, min_replicas=4, max_replicas=15, poids_ratio=2.0), Deployment(id="user-service", groupe_id="backend-group", replicas_actuels=6, min_replicas=3, max_replicas=10, poids_ratio=2.0), Deployment(id="logging-service", replicas_actuels=3, min_replicas=2, max_replicas=5), Deployment(id="critical-db", replicas_actuels=2, min_replicas=2, max_replicas=3)]
    power_source = PowerSource(optimal_power_kw=100.0, volatility=0.25)
    plotter = RealTimePlotter(deployment_ids=[d.id for d in cluster_state])
    
    cycle = 0
    try:
        while True:
            cycle += 1
            logging.info(f"===== DÉBUT DU CYCLE N°{cycle} =====")
            
            # 1. Mettre à jour la puissance et lire les valeurs
            current_power = power_source.update_and_get_power()
            reduction_percentage = power_source.get_reduction_percentage()
            
            # Affichage correct de la puissance
            logging.info(f"Puissance Actuelle: {current_power:.2f} kW / {power_source.optimal_power:.2f} kW")
            
            scaler = EnergyAwareScaler(cluster_state)
            
            # 2. Décider de l'action: scale-down ou scale-up
            if reduction_percentage > 0:
                logging.info(f"BAISSE DE PUISSANCE DÉTECTÉE: {reduction_percentage:.2%}")
                result = scaler.run_scale_down(reduction_percentage)
            else:
                logging.info("Puissance stable ou en hausse. Tentative de scale-up.")
                result = scaler.run_scale_up()

            # 3. Mettre à jour l'état du cluster si une action a eu lieu
            if result and result['final_state']:
                current_state_map = {d.id: d for d in cluster_state}
                for final_deployment_state in result['final_state']:
                    current_state_map[final_deployment_state['id']].replicas_actuels = final_deployment_state['final_replicas']

            # 4. Log et mise à jour du graphique
            logging.info("--- État actuel du cluster ---")
            total_replicas = 0
            individual_replicas = {}
            for d in cluster_state:
                logging.info(f"  - {d.id:<25} : {d.replicas_actuels} réplicas")
                total_replicas += d.replicas_actuels
                individual_replicas[d.id] = d.replicas_actuels
            
            plotter.update(cycle, power_source.optimal_power, current_power, total_replicas, individual_replicas)
            
            logging.info(f"Fin du cycle. Prochain cycle dans {SIMULATION_INTERVAL_SECONDS} secondes.")
            time.sleep(SIMULATION_INTERVAL_SECONDS)
            
    except KeyboardInterrupt:
        logging.info("Simulation terminée.")
    finally:
        if MATPLOTLIB_AVAILABLE:
            plt.ioff()
            logging.info("Graphique final. Fermez la fenêtre pour quitter.")
            plt.show()