import math
import numpy as np
import matplotlib.pyplot as plt
from matplotlib.widgets import Slider

class Deployment:
    def __init__(self, name, min_replicas, max_replicas, weight=1.0, group=None):
        self.name = name
        self.min = min_replicas
        self.max = max_replicas
        self.weight = weight
        self.group = group
        self.current = max_replicas  # Initial state: optimal power
    
    @property
    def margin_down(self):
        return self.current - self.min
    
    @property
    def margin_up(self):
        return self.max - self.current

def largest_remainder_method(shares, total_int):
    """Applique la méthode du plus grand reste pour répartir un entier total."""
    if not shares or total_int == 0:
        return [0] * len(shares)
    
    # Calcul des parties entières
    integer_parts = [math.floor(s) for s in shares]
    remainders = [s - i for s, i in zip(shares, integer_parts)]
    
    # Calcul du reste à distribuer
    current_total = sum(integer_parts)
    remaining = total_int - current_total
    
    # Distribution du reste par plus grand reste
    indices = sorted(range(len(remainders)), key=lambda i: -remainders[i])
    for i in indices[:remaining]:
        integer_parts[i] += 1
    
    return integer_parts

def adjust_replicas(deployments, groups, p):
    """Ajuste les réplicas en fonction de la variation de puissance (p)"""
    # Étape 1: Calculer l'objectif global
    M_total = sum(d.max for d in deployments)
    target_total = M_total - math.ceil(p * M_total)
    current_total = sum(d.current for d in deployments)
    delta = target_total - current_total
    
    if delta == 0:
        return 0, 0
    
    # Réduction de puissance
    if delta < 0:
        reduction_needed = -delta
        entities = []
        entity_shares = []
        entity_types = []
        
        # Ajouter les groupes
        for group_name, group_members in groups.items():
            M_g = sum(d.max for d in group_members)
            entities.append(group_name)
            entity_shares.append(M_g)
            entity_types.append('group')
        
        # Ajouter les déploiements indépendants
        independent_deploys = [d for d in deployments if d.group is None]
        for d in independent_deploys:
            entities.append(d.name)
            entity_shares.append(d.max)
            entity_types.append('deployment')
        
        # Répartition avec plus grand reste
        allocations = largest_remainder_method(
            [s / sum(entity_shares) for s in entity_shares], 
            reduction_needed
        )
        
        # Calculer la réduction par déploiement
        for d in deployments:
            d.x_voulue = 0
        
        # Traiter les groupes
        for idx, (entity, alloc) in enumerate(zip(entities, allocations)):
            if entity_types[idx] == 'group':
                group_members = groups[entity]
                W_g = sum(d.weight for d in group_members)
                if W_g > 0:
                    shares = [alloc * (d.weight / W_g) for d in group_members]
                else:
                    shares = [0] * len(group_members)
                member_allocations = largest_remainder_method(shares, alloc)
                for member, alloc_val in zip(group_members, member_allocations):
                    member.x_voulue = alloc_val
        
        # Traiter les déploiements indépendants
        for idx, (entity, alloc) in enumerate(zip(entities, allocations)):
            if entity_types[idx] == 'deployment':
                dep = next(d for d in independent_deploys if d.name == entity)
                dep.x_voulue = alloc
        
        # Ajustement final sous contraintes
        total_deficit = 0
        for d in deployments:
            x_max = d.current - d.min
            d.x_final = min(d.x_voulue, x_max)
            deficit = max(0, d.x_voulue - d.x_final)
            total_deficit += deficit
        
        # Redistribution du déficit
        while total_deficit > 0:
            donors = [d for d in deployments if d.margin_down > 0 and d.x_final < d.x_voulue]
            if not donors:
                break
            best_donor = max(donors, key=lambda d: d.margin_down)
            best_donor.x_final += 1
            total_deficit -= 1
        
        # Appliquer la réduction
        for d in deployments:
            d.current -= d.x_final
        
        return reduction_needed, reduction_needed - total_deficit
    
    # Augmentation de puissance
    else:
        increase_needed = delta
        entities = []
        entity_margins = []
        entity_types = []
        
        # Ajouter les groupes
        for group_name, group_members in groups.items():
            margin_g = sum(d.margin_up for d in group_members)
            entities.append(group_name)
            entity_margins.append(margin_g)
            entity_types.append('group')
        
        # Ajouter les déploiements indépendants
        independent_deploys = [d for d in deployments if d.group is None]
        for d in independent_deploys:
            entities.append(d.name)
            entity_margins.append(d.margin_up)
            entity_types.append('deployment')
        
        # Répartition avec plus grand reste
        total_margin = sum(entity_margins)
        if total_margin < increase_needed:
            increase_needed = total_margin  # Limite physique
        
        if total_margin > 0:
            shares = [m / total_margin * increase_needed for m in entity_margins]
        else:
            shares = [0] * len(entity_margins)
        allocations = largest_remainder_method(shares, increase_needed)
        
        # Calculer l'augmentation par déploiement
        for d in deployments:
            d.x_voulue = 0
        
        # Traiter les groupes
        for idx, (entity, alloc) in enumerate(zip(entities, allocations)):
            if entity_types[idx] == 'group':
                group_members = groups[entity]
                total_weighted_margin = sum(d.weight * d.margin_up for d in group_members)
                if total_weighted_margin > 0:
                    shares = [alloc * (d.weight * d.margin_up) / total_weighted_margin for d in group_members]
                else:
                    shares = [0] * len(group_members)
                member_allocations = largest_remainder_method(shares, alloc)
                for member, alloc_val in zip(group_members, member_allocations):
                    member.x_voulue = alloc_val
        
        # Traiter les déploiements indépendants
        for idx, (entity, alloc) in enumerate(zip(entities, allocations)):
            if entity_types[idx] == 'deployment':
                dep = next(d for d in independent_deploys if d.name == entity)
                dep.x_voulue = alloc
        
        # Ajustement final sous contraintes
        total_deficit = 0
        for d in deployments:
            x_max = d.margin_up
            d.x_final = min(d.x_voulue, x_max)
            deficit = max(0, d.x_voulue - d.x_final)
            total_deficit += deficit
        
        # Redistribution du déficit
        while total_deficit > 0:
            donors = [d for d in deployments if d.margin_up > 0 and d.x_final < d.x_voulue]
            if not donors:
                break
            best_donor = max(donors, key=lambda d: d.margin_up)
            best_donor.x_final += 1
            total_deficit -= 1
        
        # Appliquer l'augmentation
        for d in deployments:
            d.current += d.x_final
        
        return -increase_needed, -(increase_needed - total_deficit)

# Configuration initiale du cluster
deployments = [
    Deployment("Frontend", min_replicas=2, max_replicas=15, weight=0.6, group="Web"),
    Deployment("Backend", min_replicas=1, max_replicas=12, weight=0.4, group="Web"),
    Deployment("Database", min_replicas=3, max_replicas=8),
    Deployment("Monitoring", min_replicas=1, max_replicas=5)
]

groups = {
    "Web": [d for d in deployments if d.group == "Web"]
}

# Initialisation des données
power_levels = np.arange(0, 0.51, 0.05)
history = []
current_power = 0.0

# Création de la figure
fig, (ax1, ax2) = plt.subplots(2, 1, figsize=(12, 10))
plt.subplots_adjust(bottom=0.25)

# Fonction de mise à jour
def update(p):
    global current_power, deployments
    
    # Sauvegarder l'état actuel pour calcul des deltas
    prev_state = [d.current for d in deployments]
    
    # Appliquer le changement de puissance
    target, actual = adjust_replicas(deployments, groups, p)
    current_power = p
    
    # Calculer les changements
    changes = [d.current - prev for d, prev in zip(deployments, prev_state)]
    
    # Mettre à jour l'historique
    history.append({
        'p': p,
        'target': target,
        'actual': actual,
        'replicas': [d.current for d in deployments],
        'changes': changes
    })
    
    # Mettre à jour les graphiques
    ax1.clear()
    ax2.clear()
    
    # Graphique 1: Réplicas par service
    services = [d.name for d in deployments]
    colors = plt.cm.tab10.colors
    for i, service in enumerate(services):
        replicas = [h['replicas'][i] for h in history]
        ax1.plot([h['p'] for h in history], replicas, 'o-', 
                color=colors[i], label=service, linewidth=2, markersize=8)
    
    ax1.set_title('Évolution des réplicas en fonction de la puissance')
    ax1.set_xlabel('Niveau de puissance (p)')
    ax1.set_ylabel('Nombre de réplicas')
    ax1.grid(True, linestyle='--', alpha=0.7)
    ax1.legend(loc='upper right')
    
    # Graphique 2: Changements de réplicas
    width = 0.2
    for i, service in enumerate(services):
        changes = [h['changes'][i] for h in history]
        positions = np.arange(len(history)) + i * width
        ax2.bar(positions, changes, width, 
               color=colors[i], label=service)
    
    ax2.set_title('Variations des réplicas entre les paliers')
    ax2.set_xlabel('Index du palier')
    ax2.set_ylabel('Changement de réplicas')
    ax2.set_xticks(np.arange(len(history)) + width * 1.5)
    ax2.set_xticklabels([f"{h['p']:.2f}" for h in history])
    ax2.grid(True, linestyle='--', alpha=0.7)
    
    plt.draw()

# Configuration du slider
ax_slider = plt.axes([0.2, 0.1, 0.6, 0.03])
power_slider = Slider(
    ax=ax_slider,
    label='Puissance (p)',
    valmin=0.0,
    valmax=0.5,
    valinit=current_power,
    valstep=0.05
)

# Fonction de rappel pour le slider
def on_power_change(val):
    update(val)
    fig.canvas.draw_idle()

power_slider.on_changed(on_power_change)

# Initialisation
update(current_power)
plt.show()